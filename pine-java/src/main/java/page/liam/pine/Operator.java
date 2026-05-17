package page.liam.pine;

public interface Operator {
    void init(OperatorParams params);
    void execute(CancellationToken token, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException;
}
