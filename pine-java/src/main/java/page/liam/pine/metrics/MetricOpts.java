package page.liam.pine.metrics;

/**
 * Name, help text and label names for a Counter or Gauge.
 */
public class MetricOpts {
    public final String name;
    public final String help;
    public final String[] labelNames;

    public MetricOpts(String name, String help, String... labelNames) {
        this.name = name;
        this.help = help;
        this.labelNames = labelNames;
    }
}
