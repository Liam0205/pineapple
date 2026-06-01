package page.liam.pine;

import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.jupiter.api.Test;
import page.liam.pine.metrics.HistogramOpts;
import page.liam.pine.metrics.MetricOpts;
import page.liam.pine.metrics.MetricsCollector;

import java.util.Map;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Verifies the aggregating {@link MetricsCollector} snapshot shape and byte-exact
 * JSON, mirroring pine-go's TestCollectorSnapshot* so the /stats `resources`
 * subtree stays byte-aligned across runtimes.
 */
public class MetricsCollectorTest {
    private static final ObjectMapper MAPPER = GoFormat.createGoCompatMapper();

    @Test
    void snapshotShape() {
        MetricsCollector c = new MetricsCollector();
        c.newGauge(new MetricOpts("pine_redis_pool_total_conns", "test")).with("cache").set(3);
        c.newGauge(new MetricOpts("pine_redis_pool_idle_conns", "test")).with("cache").set(2);
        c.newGauge(new MetricOpts("pine_redis_up", "test")).with("cache").set(1);
        c.newHistogram(new HistogramOpts("pine_redis_ping_duration_seconds", "test", new double[]{}))
                .with("cache").observe(0.001); // 1ms → 1_000_000 ns

        Map<String, Object> snap = c.snapshot();

        @SuppressWarnings("unchecked")
        Map<String, Object> total = (Map<String, Object>) snap.get("pine_redis_pool_total_conns");
        assertEquals(3.0, total.get("cache"));

        @SuppressWarnings("unchecked")
        Map<String, Object> hist = (Map<String, Object>) snap.get("pine_redis_ping_duration_seconds");
        @SuppressWarnings("unchecked")
        Map<String, Long> cell = (Map<String, Long>) hist.get("cache");
        assertEquals(1L, cell.get("count"));
        assertEquals(1_000_000L, cell.get("sum_ns"));
    }

    @Test
    void snapshotJsonByteExact() throws Exception {
        MetricsCollector c = new MetricsCollector();
        c.newGauge(new MetricOpts("pine_redis_up", "test")).with("cache").set(1);
        c.newHistogram(new HistogramOpts("pine_redis_ping_duration_seconds", "test", new double[]{}))
                .with("cache").observe(0.0005); // 500_000 ns

        String json = MAPPER.writeValueAsString(c.snapshot());
        String want = "{\"pine_redis_ping_duration_seconds\":{\"cache\":{\"count\":1,\"sum_ns\":500000}},"
                + "\"pine_redis_up\":{\"cache\":1}}";
        assertEquals(want, json);
    }
}
