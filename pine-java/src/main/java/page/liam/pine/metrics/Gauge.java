package page.liam.pine.metrics;

public interface Gauge {
    Gauge with(String... labelValues);
    void set(double value);
    void add(double delta);
}
