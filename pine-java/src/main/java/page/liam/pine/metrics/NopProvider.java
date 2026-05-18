package page.liam.pine.metrics;

public final class NopProvider implements Provider {
    private static final NopProvider INSTANCE = new NopProvider();

    private static final Counter NOP_COUNTER = new Counter() {
        @Override
        public Counter with(String... labelValues) {
            return this;
        }

        @Override
        public void inc() {
        }
    };

    private static final Gauge NOP_GAUGE = new Gauge() {
        @Override
        public Gauge with(String... labelValues) {
            return this;
        }

        @Override
        public void set(double value) {
        }

        @Override
        public void add(double delta) {
        }
    };

    private static final Histogram NOP_HISTOGRAM = new Histogram() {
        @Override
        public Histogram with(String... labelValues) {
            return this;
        }

        @Override
        public void observe(double value) {
        }
    };

    private NopProvider() {
    }

    public static NopProvider getInstance() {
        return INSTANCE;
    }

    @Override
    public Counter newCounter(MetricOpts opts) {
        return NOP_COUNTER;
    }

    @Override
    public Gauge newGauge(MetricOpts opts) {
        return NOP_GAUGE;
    }

    @Override
    public Histogram newHistogram(HistogramOpts opts) {
        return NOP_HISTOGRAM;
    }
}
