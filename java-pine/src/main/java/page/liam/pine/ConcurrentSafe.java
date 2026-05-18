package page.liam.pine;

/**
 * Marker interface for operators that are safe to execute concurrently
 * on split item shards (data_parallel > 1).
 *
 * Requirements: operator must be stateless per-execute (no mutable shared state
 * across items), and must not write to common_output.
 */
public interface ConcurrentSafe {
}
