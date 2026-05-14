package com.tipsy.pine.operators;

import com.tipsy.pine.AbstractOperator;
import com.tipsy.pine.OperatorInput;
import com.tipsy.pine.OperatorOutput;

import java.util.Map;
import java.util.Objects;

public class FilterCondition extends AbstractOperator {
    private Object value;

    @Override
    public void init(Map<String, Object> params) {
        this.value = params.get("value");
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        String field = itemInput.get(0);
        for (int i = 0; i < input.itemCount(); i++) {
            if (Objects.equals(String.valueOf(input.item(i, field)), String.valueOf(value))) {
                output.removeItem(i);
            }
        }
    }
}
