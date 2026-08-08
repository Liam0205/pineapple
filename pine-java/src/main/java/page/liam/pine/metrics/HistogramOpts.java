package page.liam.pine.metrics;

/**
 * {@link MetricOpts} plus Pine's suggested bucket boundaries.
 */
public class HistogramOpts extends MetricOpts {
    /**
     * Pine's <b>suggested</b> boundaries. No bundled Provider reads this —
     * {@link MetricsCollector} aggregates count and sum only — so it exists as
     * advice to an external backend, which may honour, replace or ignore it.
     * Both an empty and a non-empty value really occur, so a Provider must handle
     * both: the engine and server histograms pass a suggestion, while the Redis
     * probe histograms pass none. A Provider wanting its own defaults cannot wait
     * for empty — it must actively ignore a non-empty suggestion.
     */
    public final double[] buckets;

    public HistogramOpts(String name, String help, double[] buckets, String... labelNames) {
        super(name, help, labelNames);
        this.buckets = buckets;
    }
}
