package page.liam.pine;

import java.util.List;

public abstract class AbstractOperator implements Operator, MetadataAware, LoggerAware {
    private List<String> commonInput = List.of();
    private List<String> commonOutput = List.of();
    private List<String> itemInput = List.of();
    private List<String> itemOutput = List.of();
    private String engineLogPrefix = "";

    @Override
    public final void setMetadata(List<String> commonInput, List<String> commonOutput,
                            List<String> itemInput, List<String> itemOutput) {
        this.commonInput = List.copyOf(commonInput);
        this.commonOutput = List.copyOf(commonOutput);
        this.itemInput = List.copyOf(itemInput);
        this.itemOutput = List.copyOf(itemOutput);
    }

    @Override
    public final void setEngineLogPrefix(String prefix) {
        this.engineLogPrefix = prefix == null ? "" : prefix;
    }

    protected List<String> commonInput() { return commonInput; }
    protected List<String> commonOutput() { return commonOutput; }
    protected List<String> itemInput() { return itemInput; }
    protected List<String> itemOutput() { return itemOutput; }

    /**
     * Writes a diagnostic line to stderr prefixed with the owning engine's
     * log_prefix, so multi-engine processes keep per-engine attribution
     * (issue #172). Use instead of raw System.err in operator code.
     */
    protected final void logf(String format, Object... args) {
        // The prefix is user-configured and must be emitted as a literal —
        // never concatenated into the format string, where a stray '%'
        // (e.g. "[100%] ") would throw at runtime.
        System.err.print(engineLogPrefix);
        System.err.printf(format + "%n", args);
    }
}
