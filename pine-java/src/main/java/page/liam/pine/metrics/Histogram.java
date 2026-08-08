package page.liam.pine.metrics;

/**
 * Records observations. Implementations must be thread-safe; see
 * {@link Provider} for the full implementer's contract.
 */
public interface Histogram {
    Histogram with(String... labelValues);
    /**
     * Records one value. For duration histograms the value is in <b>seconds</b>
     * — the signature cannot enforce this, and assuming milliseconds
     * misreports by 1000x.
     */
    void observe(double value);
}
