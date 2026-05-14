package com.tipsy.pine.operators;

import com.tipsy.pine.AbstractOperator;
import com.tipsy.pine.OperatorInput;
import com.tipsy.pine.OperatorOutput;

import java.util.Map;

public class TransformSize extends AbstractOperator {
    @Override
    public void init(Map<String, Object> params) {}

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        output.setCommon(commonOutput.get(0), input.itemCount());
    }
}
