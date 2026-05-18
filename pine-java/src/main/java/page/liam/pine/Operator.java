package page.liam.pine;

import java.util.Map;

public interface Operator {
    void init(Map<String, Object> params) throws Exception;
    void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws Exception;
}
