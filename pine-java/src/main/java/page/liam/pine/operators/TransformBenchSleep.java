package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.RequestContext;
import page.liam.pine.ConcurrentSafe;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import page.liam.pine.OperatorParams;
import page.liam.pine.PineErrors;

import java.time.Duration;

public class TransformBenchSleep extends AbstractOperator implements ConcurrentSafe {
    private int delayMs = 5;

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("delay_ms");
        if (v instanceof Number) {
            delayMs = ((Number) v).intValue();
        }
    }

    @Override
    public void execute(RequestContext ctx, OperatorInput input, OperatorOutput output) throws PineErrors.OperatorException {
        // Waits for the delay or the context, whichever comes first, and
        // reports the context error as pine-go does (select on ctx.Done()).
        try {
            if (ctx.awaitCancellation(Duration.ofMillis(delayMs))) {
                ctx.throwIfCancelled();
            }
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
            throw new PineErrors.OperatorException("transform_bench_sleep: interrupted", e);
        }
        int n = input.itemCount();
        for (int i = 0; i < n; i++) {
            output.setItem(i, "_bench_slept", true);
        }
    }
}
