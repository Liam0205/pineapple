package page.liam.pine.metrics;

/**
 * A cumulative metric that only goes up. Implementations must be thread-safe;
 * see {@link Provider} for the full implementer's contract.
 */
public interface Counter {
    Counter with(String... labelValues);
    void inc();
}
