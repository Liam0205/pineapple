package page.liam.pine;

import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicReference;

public class ParallelExecutor {

    private ParallelExecutor() {}

    public static OperatorOutput execute(CancellationToken token, Operator op, OperatorInput input, int parallelism) throws PineErrors.OperatorException {
        int total = input.itemCount();
        if (parallelism <= 1 || total == 0) {
            OperatorOutput output = new OperatorOutput();
            op.execute(token, input, output);
            return output;
        }

        int n = Math.min(parallelism, total);
        List<OperatorInput> shards = new ArrayList<>(n);
        int[] offsets = new int[n];

        int base = total / n;
        int rem = total % n;
        int start = 0;
        List<Map<String, Object>> allItems = input.rawItems();
        Map<String, Object> common = input.rawCommon();

        for (int i = 0; i < n; i++) {
            int size = base + (i < rem ? 1 : 0);
            int end = start + size;
            List<Map<String, Object>> shardItems = new ArrayList<>(allItems.subList(start, end));
            shards.add(new OperatorInput(common, shardItems));
            offsets[i] = start;
            start = end;
        }

        if (shards.size() == 1) {
            OperatorOutput output = new OperatorOutput();
            op.execute(token, shards.get(0), output);
            return output;
        }

        CancellationToken shardToken = new CancellationToken() {
            @Override
            public boolean isCancelled() {
                return super.isCancelled() || token.isCancelled();
            }
        };
        AtomicReference<Exception> firstError = new AtomicReference<>();
        ForkJoinPool pool = ForkJoinPool.commonPool();
        List<Future<OperatorOutput>> futures = new ArrayList<>(n);

        for (OperatorInput shard : shards) {
            futures.add(pool.submit(() -> {
                if (shardToken.isCancelled() || token.isCancelled()) return null;
                try {
                    OperatorOutput out = new OperatorOutput();
                    op.execute(shardToken, shard, out);
                    return out;
                } catch (PineErrors.OperatorException e) {
                    if (firstError.compareAndSet(null, e)) {
                        shardToken.cancel();
                    }
                    return null;
                } catch (RuntimeException e) {
                    if (firstError.compareAndSet(null, e)) {
                        shardToken.cancel();
                    }
                    return null;
                } catch (Throwable t) {
                    Exception ex = new PineErrors.PanicError("parallel-shard", t);
                    if (firstError.compareAndSet(null, ex)) {
                        shardToken.cancel();
                    }
                    return null;
                }
            }));
        }

        OperatorOutput merged = new OperatorOutput();

        for (int i = 0; i < futures.size(); i++) {
            try {
                OperatorOutput out = futures.get(i).get();
                if (out == null) continue;
                int offset = offsets[i];
                for (Map.Entry<Integer, Map<String, Object>> entry : out.getItemWrites().entrySet()) {
                    int absIdx = entry.getKey() + offset;
                    for (Map.Entry<String, Object> field : entry.getValue().entrySet()) {
                        merged.setItem(absIdx, field.getKey(), field.getValue());
                    }
                }
                if (out.getWarning() != null) {
                    merged.setWarning(out.getWarning());
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                if (firstError.compareAndSet(null, new PineErrors.OperatorException("parallel execution interrupted", e))) {
                    shardToken.cancel();
                }
            } catch (ExecutionException e) {
                Exception cause = e.getCause() instanceof Exception
                        ? (Exception) e.getCause()
                        : new PineErrors.PanicError("parallel-shard", e.getCause());
                if (firstError.compareAndSet(null, cause)) {
                    shardToken.cancel();
                    for (int j = i + 1; j < futures.size(); j++) {
                        futures.get(j).cancel(true);
                    }
                }
            }
        }

        if (firstError.get() != null) {
            Exception err = firstError.get();
            if (err instanceof PineErrors.OperatorException) {
                throw (PineErrors.OperatorException) err;
            }
            if (err instanceof RuntimeException) {
                throw (RuntimeException) err;
            }
            throw new PineErrors.OperatorException(err.getMessage(), err);
        }
        return merged;
    }
}
