package page.liam.pine;

import java.util.List;
import java.util.Map;

public interface Frame {
    Object common(String field);
    Object item(int index, String field);
    int itemCount();
    OperatorInput buildInput(String opName, InputFieldSpec spec) throws PineErrors.OperatorException;
    void applyOutput(OperatorOutput out, String opName, boolean recall);
    Map<String, Object> toResultCommon(List<String> commonOut);
    List<Map<String, Object>> toResultItems(List<String> itemOut);

    /**
     * Optional batch read: returns the [offset, offset+count) window of the
     * field's item values in one lock acquisition, with element i identical
     * to item(offset + i, field) (before item-default substitution). Returns
     * null when the frame cannot serve the window, in which case callers
     * fall back to per-element access.
     *
     * <p>The returned array is READ-ONLY and valid only for the current
     * operator execute: ColumnFrame may return its live column array
     * (zero-copy). Safety of escaping the frame lock relies on the DAG
     * scheduler hazard-ordering writers of this field and row-set mutating
     * operators relative to the reader.
     */
    default Object[] itemColumnView(String field, int offset, int count) {
        return null;
    }

    /**
     * Optional typed batch read: raw double[] window when the field is
     * stored as a typed double column AND every slot in the window is
     * present. Null = unsupported / mixed types / nulls present; callers
     * fall back to itemColumnView. Same read-only/Execute-scoped escape
     * contract as itemColumnView.
     */
    default double[] itemColumnDoubleView(String field, int offset, int count) {
        return null;
    }

    /**
     * Selects the physical frame implementation by {@code storage_mode}.
     *
     * <p>Matching is EXACT and case-sensitive, and anything other than the
     * literal {@code "column"} — including null, empty, a misspelling, and
     * {@code "Column"} — yields the row store. That mirrors pine-go's
     * {@code NewFrame}, which is a {@code switch} on a string-typed
     * {@code StorageMode} with {@code default: newRowFrame}, so only an exact
     * {@code "column"} reaches the column store.
     *
     * <p>This used to use equalsIgnoreCase, which made {@code "Column"} select
     * the column store here and the row store in Go — the same configuration
     * meaning different things in different runtimes (issue #179). Row/column
     * output parity means that never changed a response, only the memory and
     * performance profile, which is why it went unnoticed.
     *
     * <p>Deliberately a silent fallback rather than a rejection: Go's default
     * branch accepts anything, so rejecting here would itself be a divergence.
     * Rejecting invalid values in all three runtimes is a separate decision,
     * recorded in llmdoc/reference/storage-mode-dispatch.md.
     */
    static Frame create(String storageMode, Map<String, Object> common, List<Map<String, Object>> items) {
        if ("column".equals(storageMode)) {
            return new ColumnFrame(common, items);
        }
        return new DataFrame(common, items);
    }
}
