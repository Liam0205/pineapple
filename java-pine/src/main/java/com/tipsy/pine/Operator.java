package com.tipsy.pine;

import java.util.Map;

public interface Operator {
    void init(Map<String, Object> params) throws Exception;
    void execute(OperatorInput input, OperatorOutput output) throws Exception;
}
