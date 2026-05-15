package page.liam.pine;

/**
 * Lightweight cancellation signal, analogous to Go's context.Context cancel channel.
 * Operators should periodically check isCancelled() during long operations.
 */
public class CancellationToken {
    private volatile boolean cancelled;

    public boolean isCancelled() { return cancelled; }
    public void cancel() { cancelled = true; }
}
