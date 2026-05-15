package page.liam.pine.metrics;

public interface Provider {
    Counter newCounter(MetricOpts opts);
    Gauge newGauge(MetricOpts opts);
    Histogram newHistogram(HistogramOpts opts);
}
