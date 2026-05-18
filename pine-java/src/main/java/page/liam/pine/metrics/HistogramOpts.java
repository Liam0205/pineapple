package page.liam.pine.metrics;

public class HistogramOpts extends MetricOpts {
    public final double[] buckets;

    public HistogramOpts(String name, String help, double[] buckets, String... labelNames) {
        super(name, help, labelNames);
        this.buckets = buckets;
    }
}
