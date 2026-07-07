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
        String want = GoFormat.sprint(value);
        Object[] col = input.itemColumn(field);
        for (int i = 0; i < col.length; i++) {
            if (Objects.equals(GoFormat.sprint(col[i]), want)) {
                output.removeItem(i);
            }
        }
    }
}
