package page.liam.pine;

public interface Operator {
    void init(OperatorParams params);

    /**
     * Runs the operator for one request. {@code ctx} carries the request's
     * cancellation, deadline, values and resources ({@link RequestContext}).
     * An operator that waits or loops should stop once {@code ctx} is done,
     * typically by {@link RequestContext#throwIfCancelled()}.
     */
    void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException;
}
