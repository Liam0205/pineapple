package page.liam.pine.metrics;

/**
 * {@link MetricOpts} plus Pine's suggested bucket boundaries.
 */
public class HistogramOpts extends MetricOpts {
    /**
     * Pine's <b>suggested</b> boundaries. No bundled Provider reads this —
     * {@link MetricsCollector} aggregates count and sum only — so it exists as
     * advice to an external backend, which may honour, replace or ignore it.
     * Pine's engine always passes a non-empty suggestion, so a Provider wanting
     * its own defaults must actively ignore this field.
     */
    public final double[] buckets;

    public HistogramOpts(String name, String help, double[] buckets, String... labelNames) {
        super(name, help, labelNames);
        this.buckets = buckets;
    }
}
