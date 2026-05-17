package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorParams;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorParams;
import page.liam.pine.OperatorOutput;
import page.liam.pine.OperatorParams;

import java.util.Map;

/**
 * Operator: transform_size
 * Metadata contract
 *   CommonInput:  []
 *   CommonOutput: [<target_field>]
 *   ItemInput:    []
 *   ItemOutput:   []
 */
public class TransformSize extends AbstractOperator implements page.liam.pine.ConcurrentSafe {
    @Override
    public void init(OperatorParams params) {}

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        output.setCommon(commonOutput().get(0), input.itemCount());
    }
}
