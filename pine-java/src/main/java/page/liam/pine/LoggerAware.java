package page.liam.pine;

/**
 * Optional interface for operators that emit diagnostic log lines. The
 * engine calls {@link #setEngineLogPrefix} after {@code init} with its
 * resolved log_prefix (option &gt; JSON config), so operator output carries
 * the owning engine's prefix even when multiple engines run in one process
 * (issue #172). Mirrors pine-go's LoggerAware/LoggerHolder injection.
 */
public interface LoggerAware {
    void setEngineLogPrefix(String prefix);
}
