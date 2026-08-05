package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorParams;
import page.liam.pine.GoFormat;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.Map;
import java.util.Objects;

/**
 * Operator: filter_condition
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: []
 *   ItemInput:    [<field>]
 *   ItemOutput:   []
 */
public class FilterCondition extends AbstractOperator implements page.liam.pine.ConsumesRowSet, page.liam.pine.MutatesRowSet {
    private Object value;

    @Override
    public void init(OperatorParams params) {
        this.value = params.get("value");
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        String field = itemInput().get(0);
        // Normalize BOTH sides through the same representation before comparing.
        // GoFormat.sprint preserves the box type on purpose, because Go's %v prints a
        // native int plainly and other consumers (Redis keys, Redis member values,
        // templated params) depend on that. But Jackson decodes a config literal to
        // Integer and pipeline data to Double for the SAME number, so comparing raw
        // sprint output made the two sides obey different rules: value 2000000 stopped
        // matching a Lua-produced 2000000 once the latter arrived as a Double.
        //
        // Go has no such asymmetry — both of its sides came through encoding/json as
        // float64 — so the fix belongs here, at the comparison, not in the shared
        // formatter. Issues #189/#190.
        String want = GoFormat.sprint(normalizeForCompare(value));
        Object[] col = input.itemColumn(field);
        for (int i = 0; i < col.length; i++) {
            if (Objects.equals(GoFormat.sprint(normalizeForCompare(col[i])), want)) {
                output.removeItem(i);
            }
        }
    }

    /**
     * Collapses any numeric box to double so both sides of the comparison format under
     * one rule, mirroring Go where every compared value is a float64. Non-numbers pass
     * through untouched, so string and boolean identity is unaffected.
     */
    private static Object normalizeForCompare(Object v) {
        return v instanceof Number ? ((Number) v).doubleValue() : v;
    }
}
