package page.liam.pine.operators;

import page.liam.pine.AbstractOperator;
import page.liam.pine.CancellationToken;
import page.liam.pine.ConcurrentSafe;
import page.liam.pine.OperatorInput;
import page.liam.pine.OperatorOutput;
import page.liam.pine.OperatorParams;

public class TransformBenchCpu extends AbstractOperator implements ConcurrentSafe {
    private int iterations = 100;

    @Override
    public void init(OperatorParams params) {
        Object v = params.get("iterations");
        if (v instanceof Number) {
            iterations = ((Number) v).intValue();
        }
    }

    @Override
    public void execute(CancellationToken token, OperatorInput input, OperatorOutput output) {
        int n = input.itemCount();
        for (int i = 0; i < n; i++) {
            double result = cpuWork(iterations);
            output.setItem(i, "_bench_result", result);
        }
    }

    private static double cpuWork(int iterations) {
        double acc = 0;
        for (int i = 0; i < iterations; i++) {
            acc += fib(32);
            acc /= 1.000001;
        }
        return acc;
    }

    private static long fib(int n) {
        if (n <= 1) return n;
        long a = 0, b = 1;
        for (int i = 2; i <= n; i++) {
            long tmp = a + b;
            a = b;
            b = tmp;
        }
        return b;
    }
}
