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

    static Frame create(String storageMode, Map<String, Object> common, List<Map<String, Object>> items) {
        if ("column".equalsIgnoreCase(storageMode)) {
            return new ColumnFrame(common, items);
        }
        return new DataFrame(common, items);
    }
}
