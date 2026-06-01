package page.liam.pine.metrics;

import org.junit.jupiter.api.Test;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Verifies {@link TeeProvider} fans every metric op out to all underlying
 * providers, mirroring pine-go's TestTeeFansOut / TestTeeWithCollector.
 */
public class TeeProviderTest {

    private static final class RecProvider implements Provider {
        int counterIncs;
        final List<Double> gaugeSets = new ArrayList<>();
        final List<Double> histObs = new ArrayList<>();
        String[] lastLabels;

        @Override
        public Counter newCounter(MetricOpts opts) {
            return new Counter() {
                @Override public Counter with(String... lv) { lastLabels = lv; return this; }
                @Override public void inc() { counterIncs++; }
            };
        }

        @Override
        public Gauge newGauge(MetricOpts opts) {
            return new Gauge() {
                @Override public Gauge with(String... lv) { lastLabels = lv; return this; }
                @Override public void set(double v) { gaugeSets.add(v); }
                @Override public void add(double d) {}
            };
        }

        @Override
        public Histogram newHistogram(HistogramOpts opts) {
            return new Histogram() {
                @Override public Histogram with(String... lv) { lastLabels = lv; return this; }
                @Override public void observe(double v) { histObs.add(v); }
            };
        }
    }

    @Test
    void fansOut() {
        RecProvider a = new RecProvider();
        RecProvider b = new RecProvider();
        Provider tee = new TeeProvider(a, b);

        Counter c = tee.newCounter(new MetricOpts("x", "")).with("l");
        c.inc();
        c.inc();
        assertEquals(2, a.counterIncs);
        assertEquals(2, b.counterIncs);

        Gauge g = tee.newGauge(new MetricOpts("g", "")).with("cache");
        g.set(5);
        assertEquals(List.of(5.0), a.gaugeSets);
        assertEquals(List.of(5.0), b.gaugeSets);
        assertEquals("cache", a.lastLabels[0]);
        assertEquals("cache", b.lastLabels[0]);

        Histogram h = tee.newHistogram(new HistogramOpts("h", "", new double[]{}));
        h.observe(0.001);
        assertEquals(1, a.histObs.size());
        assertEquals(1, b.histObs.size());
    }

    @Test
    void teeWithCollector() {
        RecProvider rec = new RecProvider();
        MetricsCollector col = new MetricsCollector();
        Provider tee = new TeeProvider(rec, col);

        tee.newGauge(new MetricOpts("pine_redis_up", "")).with("cache").set(1);

        assertEquals(List.of(1.0), rec.gaugeSets);
        @SuppressWarnings("unchecked")
        Map<String, Object> up = (Map<String, Object>) col.snapshot().get("pine_redis_up");
        assertEquals(1.0, up.get("cache"));
    }
}
