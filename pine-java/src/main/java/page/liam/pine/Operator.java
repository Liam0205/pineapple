package page.liam.pine;

import java.util.Map;

public interface Operator {
    void init(Map<String, Object> params);
    void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException;
}
