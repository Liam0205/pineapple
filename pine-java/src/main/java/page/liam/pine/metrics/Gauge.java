package page.liam.pine.metrics;

/**
 * A metric that can go up and down. Implementations must be thread-safe; see
 * {@link Provider} for the full implementer's contract.
 */
public interface Gauge {
    Gauge with(String... labelValues);
    void set(double value);
    void add(double delta);
}
