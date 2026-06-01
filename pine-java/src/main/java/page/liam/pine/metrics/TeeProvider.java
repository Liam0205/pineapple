package page.liam.pine.metrics;

/**
 * Provider that fans out every metric to all the given providers. Each created
 * Counter/Gauge/Histogram forwards with/inc/set/add/observe to the corresponding
 * metric from every underlying provider.
 *
 * <p>The bundled server uses it to hand the ResourceManager a provider that
 * writes both to the caller-injected Provider (e.g. a Prometheus adapter) and to
 * a dedicated in-memory {@link MetricsCollector} exposed under
 * {@code /stats.resources} — so resource metrics reach Prometheus AND /stats
 * without the engine's own metrics leaking into the resources subtree (only the
 * ResourceManager writes through the tee). Mirrors pine-go's {@code metrics.Tee}.
 */
public final class TeeProvider implements Provider {
    private final Provider[] providers;

    public TeeProvider(Provider... providers) {
        this.providers = providers;
    }

    @Override
    public Counter newCounter(MetricOpts opts) {
        Counter[] cs = new Counter[providers.length];
        for (int i = 0; i < providers.length; i++) {
            cs[i] = providers[i].newCounter(opts);
        }
        return new TeeCounter(cs);
    }

    @Override
    public Gauge newGauge(MetricOpts opts) {
        Gauge[] gs = new Gauge[providers.length];
        for (int i = 0; i < providers.length; i++) {
            gs[i] = providers[i].newGauge(opts);
        }
        return new TeeGauge(gs);
    }

    @Override
    public Histogram newHistogram(HistogramOpts opts) {
        Histogram[] hs = new Histogram[providers.length];
        for (int i = 0; i < providers.length; i++) {
            hs[i] = providers[i].newHistogram(opts);
        }
        return new TeeHistogram(hs);
    }

    private static final class TeeCounter implements Counter {
        private final Counter[] counters;

        TeeCounter(Counter[] counters) {
            this.counters = counters;
        }

        @Override
        public Counter with(String... labelValues) {
            Counter[] cs = new Counter[counters.length];
            for (int i = 0; i < counters.length; i++) {
                cs[i] = counters[i].with(labelValues);
            }
            return new TeeCounter(cs);
        }

        @Override
        public void inc() {
            for (Counter c : counters) {
                c.inc();
            }
        }
    }

    private static final class TeeGauge implements Gauge {
        private final Gauge[] gauges;

        TeeGauge(Gauge[] gauges) {
            this.gauges = gauges;
        }

        @Override
        public Gauge with(String... labelValues) {
            Gauge[] gs = new Gauge[gauges.length];
            for (int i = 0; i < gauges.length; i++) {
                gs[i] = gauges[i].with(labelValues);
            }
            return new TeeGauge(gs);
        }

        @Override
        public void set(double value) {
            for (Gauge g : gauges) {
                g.set(value);
            }
        }

        @Override
        public void add(double delta) {
            for (Gauge g : gauges) {
                g.add(delta);
            }
        }
    }

    private static final class TeeHistogram implements Histogram {
        private final Histogram[] histograms;

        TeeHistogram(Histogram[] histograms) {
            this.histograms = histograms;
        }

        @Override
        public Histogram with(String... labelValues) {
            Histogram[] hs = new Histogram[histograms.length];
            for (int i = 0; i < histograms.length; i++) {
                hs[i] = histograms[i].with(labelValues);
            }
            return new TeeHistogram(hs);
        }

        @Override
        public void observe(double value) {
            for (Histogram h : histograms) {
                h.observe(value);
            }
        }
    }
}
