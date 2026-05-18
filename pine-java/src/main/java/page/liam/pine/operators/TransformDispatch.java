package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;

import java.util.Map;

public class TransformDispatch extends AbstractOperator implements page.liam.pine.ConcurrentSafe {
    @Override
    public void init(Map<String, Object> params) {}

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        String commonField = commonInput.get(0);
        String itemField = itemOutput.get(0);
        Object val = input.common(commonField);
        for (int i = 0; i < input.itemCount(); i++) {
            output.setItem(i, itemField, val);
        }
    }
}
