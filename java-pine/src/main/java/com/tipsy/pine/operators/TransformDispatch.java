package com.tipsy.pine.operators;

import com.tipsy.pine.AbstractOperator;
import com.tipsy.pine.OperatorInput;
import com.tipsy.pine.OperatorOutput;

import java.util.Map;

public class TransformDispatch extends AbstractOperator {
    @Override
    public void init(Map<String, Object> params) {}

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        String commonField = commonInput.get(0);
        String itemField = itemOutput.get(0);
        Object val = input.common(commonField);
        for (int i = 0; i < input.itemCount(); i++) {
            output.setItem(i, itemField, val);
        }
    }
}
