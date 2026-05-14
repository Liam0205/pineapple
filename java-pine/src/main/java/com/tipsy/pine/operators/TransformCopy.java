package com.tipsy.pine.operators;

import com.tipsy.pine.AbstractOperator;
import com.tipsy.pine.OperatorInput;
import com.tipsy.pine.OperatorOutput;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;

public class TransformCopy extends AbstractOperator {
    private String direction;

    @Override
    public void init(Map<String, Object> params) throws Exception {
        direction = (String) params.get("direction");
        switch (direction) {
            case "common_to_item":
            case "item_to_common":
            case "common_to_common":
            case "item_to_item":
                break;
            default:
                throw new IllegalArgumentException("transform_copy: unsupported direction: " + direction);
        }
    }

    @Override
    public void execute(OperatorInput input, OperatorOutput output) {
        switch (direction) {
            case "common_to_common":
                for (int i = 0; i < commonInput.size(); i++) {
                    output.setCommon(commonOutput.get(i), input.common(commonInput.get(i)));
                }
                break;

            case "common_to_item":
                for (int i = 0; i < commonInput.size(); i++) {
                    Object val = input.common(commonInput.get(i));
                    String dst = itemOutput.get(i);
                    for (int j = 0; j < input.itemCount(); j++) {
                        output.setItem(j, dst, val);
                    }
                }
                break;

            case "item_to_item":
                for (int i = 0; i < itemInput.size(); i++) {
                    String src = itemInput.get(i);
                    String dst = itemOutput.get(i);
                    for (int j = 0; j < input.itemCount(); j++) {
                        output.setItem(j, dst, input.item(j, src));
                    }
                }
                break;

            case "item_to_common":
                for (int i = 0; i < itemInput.size(); i++) {
                    String src = itemInput.get(i);
                    List<Object> vals = new ArrayList<>();
                    for (int j = 0; j < input.itemCount(); j++) {
                        vals.add(input.item(j, src));
                    }
                    output.setCommon(commonOutput.get(i), vals);
                }
                break;
        }
    }
}
