package page.liam.pine;

/**
 * Marker interface for operators that change which items exist or their order
 * (filter, merge, reorder).
 *
 * DAG effect: performs a mutating write to the {@code _row_set_} sentinel,
 * forcing all prior readers and writers to complete before this operator starts,
 * and all subsequent row-set consumers to wait for it.
 */
public interface MutatesRowSet {
}
