package page.liam.pine.metrics;

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
