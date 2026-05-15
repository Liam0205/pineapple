package page.liam.pine.metrics;

public interface Counter {
    Counter with(String... labelValues);
    void inc();
}
