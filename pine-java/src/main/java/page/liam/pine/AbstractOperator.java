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
        // Format the body first, then emit prefix + body + newline in ONE
        // print call: PrintStream only serializes within a single call, so
        // two calls could interleave with another engine's output and break
        // exactly the per-engine attribution this exists for. The prefix is
        // user-configured and stays out of the format string — a stray '%'
        // (e.g. "[100%] ") in it must not throw.
        System.err.print(engineLogPrefix + String.format(format, args) + System.lineSeparator());
    }
}
