package page.liam.pine.metrics;

public interface Histogram {
    Histogram with(String... labelValues);
    void observe(double value);
}
