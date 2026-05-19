package page.liam.pine;

/**
 * Marker interface for operators that append new items to the row set
 * without reading or modifying existing items.
 *
 * DAG effect: performs an additive write to the {@code _row_set_} sentinel.
 * Additive writers run in parallel with each other, but create ordering
 * constraints with readers (ConsumesRowSet) and mutating writers (MutatesRowSet).
 *
 * Mutually exclusive with {@link MutatesRowSet}.
 */
public interface AdditiveWritesRowSet {
}
