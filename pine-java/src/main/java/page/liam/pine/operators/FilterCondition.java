package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
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
public class FilterCondition extends AbstractOperator {
    private Object value;

    @Override
    public void init(Map<String, Object> params) {
        this.value = params.get("value");
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        String field = itemInput().get(0);
        for (int i = 0; i < input.itemCount(); i++) {
            if (Objects.equals(GoFormat.sprint(input.item(i, field)), GoFormat.sprint(value))) {
                output.removeItem(i);
            }
        }
    }
}
