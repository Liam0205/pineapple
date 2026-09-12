package page.liam.pine;

import java.util.Collection;
import java.util.HashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.TreeSet;

/**
 * Enforces that an operator only wrote fields it declared in {@code $metadata}.
 *
 * <p>Mirrors pine-go {@code types.ValidateDeclaredOutputs}
 * (internal/types/operator.go, immediately after {@code ValidateOutput}):
 * common writes are checked against {@code common_output}, and item writes
 * ({@code setItem}, {@code setItemColumnDouble}, {@code addItem}) against
 * {@code item_output}.
 *
 * <p>The DAG's hazard inference is derived entirely from the declared field
 * lists, so a write to an undeclared field carries no RAW/WAW/WAR edge:
 * nothing orders it against a concurrent writer of the same name, and a
 * downstream operator that declares the field as input gets no dependency on
 * the producer. Enforcing the declaration turns that silent ordering hazard
 * into a deterministic error at the same point where the operator-type method
 * restrictions are enforced (see {@link OperatorType#validateOutput}).
 *
 * <p>{@code _source} needs no exemption: it is injected into recall-added
 * items inside {@code DataFrame/ColumnFrame.applyOutput}, which runs after
 * this check, so it is never present in {@code getAddedItems()} here.
 */
final class OutputContract {
    private OutputContract() {
    }

    /**
     * Returns the inner violation message, or {@code null} when every written
     * field is declared. The caller prefixes {@code "output contract
     * violation: "}, matching Go's wrap at the same call site.
     *
     * <p>The common channel is checked first and reported alone, matching Go.
     * Field names are reported in ascending UTF-8 byte order — Go uses
     * {@code sort.Strings}, which compares bytes; {@code String.compareTo}
     * compares UTF-16 code units and disagrees outside the BMP, so this uses
     * {@link GoFormat#compareUtf8}.
     */
    static String validateDeclaredOutputs(OperatorOutput out, List<String> commonOutput,
                                          List<String> itemOutput) {
        Set<String> undeclaredCommon = undeclaredCommonWrites(out, commonOutput);
        if (!undeclaredCommon.isEmpty()) {
            return "operator wrote undeclared common output field(s) " + formatList(undeclaredCommon);
        }
        Set<String> undeclaredItem = undeclaredItemWrites(out, itemOutput);
        if (!undeclaredItem.isEmpty()) {
            return "operator wrote undeclared item output field(s) " + formatList(undeclaredItem);
        }
        return null;
    }

    private static Set<String> undeclaredCommonWrites(OperatorOutput out, List<String> commonOutput) {
        Set<String> undeclared = newSortedSet();
        if (out.getCommonWrites().isEmpty()) {
            return undeclared;
        }
        Set<String> declared = declaredSet(commonOutput);
        for (String field : out.getCommonWrites().keySet()) {
            if (!declared.contains(field)) {
                undeclared.add(field);
            }
        }
        return undeclared;
    }

    private static Set<String> undeclaredItemWrites(OperatorOutput out, List<String> itemOutput) {
        Set<String> undeclared = newSortedSet();
        if (out.getItemWrites().isEmpty() && out.getColumnWrites().isEmpty()
                && out.getAddedItems().isEmpty()) {
            return undeclared;
        }
        Set<String> declared = declaredSet(itemOutput);
        for (Map<String, Object> perIndex : out.getItemWrites().values()) {
            collectUndeclared(perIndex.keySet(), declared, undeclared);
        }
        for (OperatorOutput.DoubleColumnWrite cw : out.getColumnWrites()) {
            if (!declared.contains(cw.field)) {
                undeclared.add(cw.field);
            }
        }
        for (Map<String, Object> added : out.getAddedItems()) {
            collectUndeclared(added.keySet(), declared, undeclared);
        }
        return undeclared;
    }

    private static void collectUndeclared(Collection<String> fields, Set<String> declared,
                                          Set<String> undeclared) {
        for (String field : fields) {
            if (!declared.contains(field)) {
                undeclared.add(field);
            }
        }
    }

    private static Set<String> declaredSet(List<String> fields) {
        // A plain HashSet: membership only, never iterated for output.
        return fields == null ? new HashSet<>() : new HashSet<>(fields);
    }

    /**
     * Sorted, de-duplicating accumulator. The sort key is the UTF-8 byte order
     * Go reports in; de-duplication matters because the same field name can
     * arrive from several item write paths (per-element, whole-column, added
     * item) within one output.
     */
    private static Set<String> newSortedSet() {
        return new TreeSet<>(GoFormat::compareUtf8);
    }

    /**
     * Formats as Go's {@code %v} of a {@code []string}: square brackets, single
     * space separator, no quotes and no commas. Identical to the list shape
     * {@link OperatorType#validateOutput} already emits.
     */
    private static String formatList(Set<String> fields) {
        StringBuilder sb = new StringBuilder("[");
        boolean first = true;
        for (String field : fields) {
            if (!first) {
                sb.append(' ');
            }
            first = false;
            sb.append(field);
        }
        sb.append(']');
        return sb.toString();
    }
}
