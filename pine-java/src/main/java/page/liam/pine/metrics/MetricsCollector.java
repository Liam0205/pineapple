package page.liam.pine.metrics;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.TreeMap;

/**
 * Provider that aggregates observations in memory so resource-level metrics
 * (e.g. redis pool gauges + PING-probe latency) can be exported through the
 * bundled server's {@code /stats} endpoint without an external Prometheus
 * backend. Mirrors pine-go {@code pkg/metrics.Collector} and the C++
 * {@code metrics::Collector}.
 *
 * <p>{@link #snapshot()} returns a deterministic, JSON-serializable view shaped
 * like the http subtree: metric name -&gt; label-value key -&gt; value.
 * Counters/gauges hold a double; histograms hold {@code {count, sum_ns}} with
 * integer nanoseconds. The label-value key joins a metric's label values with a
 * single space (matching the http convention); a metric with no labels uses the
 * empty string {@code ""}. Every map level is a {@link TreeMap}, so a
 * JSON encoder that preserves insertion order emits keys lexicographically,
 * matching Go's {@code encoding/json} for {@code map[string]any}.
 */
public final class MetricsCollector implements Provider {
    private final Object lock = new Object();
    private final Map<String, Map<String, double[]>> counters = new TreeMap<>();
    private final Map<String, Map<String, double[]>> gauges = new TreeMap<>();
    private final Map<String, Map<String, HistCell>> histograms = new TreeMap<>();

    private static final class HistCell {
        long count;
        long sumNs;
    }

    @Override
    public Counter newCounter(MetricOpts opts) {
        synchronized (lock) {
            counters.computeIfAbsent(opts.name, k -> new TreeMap<>());
        }
        return new CollectorCounter(opts.name, "");
    }

    @Override
    public Gauge newGauge(MetricOpts opts) {
        synchronized (lock) {
            gauges.computeIfAbsent(opts.name, k -> new TreeMap<>());
        }
        return new CollectorGauge(opts.name, "");
    }

    @Override
    public Histogram newHistogram(HistogramOpts opts) {
        synchronized (lock) {
            histograms.computeIfAbsent(opts.name, k -> new TreeMap<>());
        }
        return new CollectorHistogram(opts.name, "");
    }

    private static String labelKey(String[] values) {
        StringBuilder sb = new StringBuilder();
        for (int i = 0; i < values.length; i++) {
            if (i > 0) {
                sb.append(' ');
            }
            sb.append(values[i]);
        }
        return sb.toString();
    }

    /**
     * Returns the current aggregated values. Counters/gauges serialize as a
     * double; histograms as {@code {count, sum_ns}}. Output is deterministic
     * (TreeMap ordering) so the byte stream matches the Go/C++ collectors.
     */
    public Map<String, Object> snapshot() {
        Map<String, Object> out = new TreeMap<>();
        synchronized (lock) {
            for (Map.Entry<String, Map<String, double[]>> e : counters.entrySet()) {
                out.put(e.getKey(), scalarView(e.getValue()));
            }
            for (Map.Entry<String, Map<String, double[]>> e : gauges.entrySet()) {
                out.put(e.getKey(), scalarView(e.getValue()));
            }
            for (Map.Entry<String, Map<String, HistCell>> e : histograms.entrySet()) {
                Map<String, Object> m = new TreeMap<>();
                for (Map.Entry<String, HistCell> c : e.getValue().entrySet()) {
                    Map<String, Long> cell = new LinkedHashMap<>();
                    cell.put("count", c.getValue().count);
                    cell.put("sum_ns", c.getValue().sumNs);
                    m.put(c.getKey(), cell);
                }
                out.put(e.getKey(), m);
            }
        }
        return out;
    }

    private static Map<String, Object> scalarView(Map<String, double[]> cells) {
        Map<String, Object> m = new TreeMap<>();
        for (Map.Entry<String, double[]> c : cells.entrySet()) {
            m.put(c.getKey(), c.getValue()[0]);
        }
        return m;
    }

    private final class CollectorCounter implements Counter {
        private final String name;
        private final String key;

        CollectorCounter(String name, String key) {
            this.name = name;
            this.key = key;
        }

        @Override
        public Counter with(String... labelValues) {
            return new CollectorCounter(name, labelKey(labelValues));
        }

        @Override
        public void inc() {
            synchronized (lock) {
                counters.get(name).computeIfAbsent(key, k -> new double[1])[0]++;
            }
        }
    }

    private final class CollectorGauge implements Gauge {
        private final String name;
        private final String key;

        CollectorGauge(String name, String key) {
            this.name = name;
            this.key = key;
        }

        @Override
        public Gauge with(String... labelValues) {
            return new CollectorGauge(name, labelKey(labelValues));
        }

        @Override
        public void set(double value) {
            synchronized (lock) {
                gauges.get(name).computeIfAbsent(key, k -> new double[1])[0] = value;
            }
        }

        @Override
        public void add(double delta) {
            synchronized (lock) {
                gauges.get(name).computeIfAbsent(key, k -> new double[1])[0] += delta;
            }
        }
    }

    private final class CollectorHistogram implements Histogram {
        private final String name;
        private final String key;

        CollectorHistogram(String name, String key) {
            this.name = name;
            this.key = key;
        }

        @Override
        public Histogram with(String... labelValues) {
            return new CollectorHistogram(name, labelKey(labelValues));
        }

        // Takes a value in seconds (the Provider convention for duration
        // histograms) and accumulates it as integer nanoseconds, matching the
        // http subtree's sum_ns so /stats stays float-free and byte-exact.
        @Override
        public void observe(double value) {
            synchronized (lock) {
                HistCell cell = histograms.get(name).computeIfAbsent(key, k -> new HistCell());
                cell.count++;
                cell.sumNs += Math.round(value * 1e9);
            }
        }
    }
}
