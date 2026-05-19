package page.liam.pine;

/**
 * Marker interface for operators that iterate items and need the row set
 * to be stable before execution.
 *
 * DAG effect: reads the {@code _row_set_} sentinel, creating a dependency
 * on all prior recalls (additive writers) and any prior MutatesRowSet operator.
 */
public interface ConsumesRowSet {
}
