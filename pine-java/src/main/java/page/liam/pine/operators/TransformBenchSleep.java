package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.ConcurrentSafe;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import page.liam.pine.OperatorParams;

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
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        try {
            Thread.sleep(delayMs);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
        int n = input.itemCount();
        for (int i = 0; i < n; i++) {
            output.setItem(i, "_bench_slept", true);
        }
    }
}
