package page.liam.pine;

import java.util.*;
import java.util.concurrent.*;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicReference;

public class ParallelExecutor {

    private ParallelExecutor() {}

    public static OperatorOutput execute(RequestContext ctx, Operator op, OperatorInput input, int parallelism, String operatorName, ExecutorService executor) throws PineErrors.OperatorException {
        int total = input.itemCount();
        if (parallelism <= 1 || total == 0) {
            OperatorOutput output = new OperatorOutput();
            op.execute(ctx, input, output);
            return output;
        }

        int n = Math.min(parallelism, total);
        List<OperatorInput> shards = new ArrayList<>(n);
        int[] offsets = new int[n];

        int base = total / n;
        int rem = total % n;
        int start = 0;
        // Propagate the resolved {{field}} map (issue #74) to every shard.
        // The map is read-only past resolution, so sharing by reference is safe.
        Map<String, Object> templated = input.rawTemplated();

        if (input.isLazy()) {
            Frame frame = input.lazyFrame();
            Map<String, Object> common = input.rawCommon();
            Map<String, Object> itemDefaults = input.lazyItemDefaults();
            int baseOffset = input.lazyOffset();
            for (int i = 0; i < n; i++) {
                int size = base + (i < rem ? 1 : 0);
                OperatorInput shard = new OperatorInput(common, frame, itemDefaults, baseOffset + start, size);
                shard.setTemplatedParams(templated);
                shards.add(shard);
                offsets[i] = start;
                start += size;
            }
        } else {
            List<Map<String, Object>> allItems = input.rawItems();
            Map<String, Object> common = input.rawCommon();
            for (int i = 0; i < n; i++) {
                int size = base + (i < rem ? 1 : 0);
                int end = start + size;
                List<Map<String, Object>> shardItems = new ArrayList<>(allItems.subList(start, end));
                OperatorInput shard = new OperatorInput(common, shardItems);
                shard.setTemplatedParams(templated);
                shards.add(shard);
                offsets[i] = start;
                start = end;
            }
        }

        if (shards.size() == 1) {
            OperatorOutput output = new OperatorOutput();
            op.execute(ctx, shards.get(0), output);
            return output;
        }

        // shardCtx is cancelled by the first shard failure (stopping the
        // others) or by the run context.
        RequestContext.Cancelable shardCtx = RequestContext.withCancel(ctx);
        try {
            return runShards(shardCtx, op, shards, offsets, operatorName, executor);
        } finally {
            shardCtx.cancel();
        }
    }

    private static OperatorOutput runShards(RequestContext.Cancelable shardCtx, Operator op,
                                            List<OperatorInput> shards, int[] offsets, String operatorName,
                                            ExecutorService executor) throws PineErrors.OperatorException {
        int n = shards.size();
        AtomicReference<Exception> firstError = new AtomicReference<>();
        // Set when a shard never ran because shardCtx was cancelled. Without
        // a shard error that is the run being cancelled, and merging the
        // remaining shards would report a partial write set as the
        // operator's complete output.
        AtomicBoolean skipped = new AtomicBoolean();
        List<Future<OperatorOutput>> futures = new ArrayList<>(n);

        for (OperatorInput shard : shards) {
            futures.add(executor.submit(() -> {
                if (shardCtx.isCancelled()) {
                    skipped.set(true);
                    return null;
                }
                try {
                    OperatorOutput out = new OperatorOutput();
                    op.execute(shardCtx, shard, out);
                    return out;
                } catch (PineErrors.OperatorException e) {
                    if (firstError.compareAndSet(null, e)) {
                        shardCtx.cancel();
                    }
                    return null;
                } catch (RuntimeException e) {
                    if (firstError.compareAndSet(null, e)) {
                        shardCtx.cancel();
                    }
                    return null;
                } catch (Throwable t) {
                    Exception ex = new PineErrors.PanicError(operatorName, t);
                    if (firstError.compareAndSet(null, ex)) {
                        shardCtx.cancel();
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
                if (!out.getAddedItems().isEmpty() || out.getItemOrder() != null ||
                        !out.getCommonWrites().isEmpty()) {
                    throw new PineErrors.PanicError(operatorName,
                        new RuntimeException(
                            "data_parallel shard emitted added_items, item_order, or common writes; " +
                            "only item_writes / removed_items / warnings are allowed"));
                }
                int offset = offsets[i];
                for (Map.Entry<Integer, Map<String, Object>> entry : out.getItemWrites().entrySet()) {
                    int absIdx = entry.getKey() + offset;
                    for (Map.Entry<String, Object> field : entry.getValue().entrySet()) {
                        merged.setItem(absIdx, field.getKey(), field.getValue());
                    }
                }
                // Shard-local whole-column writes cannot adopt into the
                // parent frame (each shard covers a window), so fold them
                // into per-element writes with the shard offset applied.
                for (OperatorOutput.DoubleColumnWrite cw : out.getColumnWrites()) {
                    for (int localIdx = 0; localIdx < cw.vals.length; localIdx++) {
                        merged.setItem(localIdx + offset, cw.field, cw.vals[localIdx]);
                    }
                }
                for (Integer localIdx : out.getRemovedItems()) {
                    merged.removeItem(localIdx + offset);
                }
                if (out.getWarning() != null) {
                    merged.setWarning(out.getWarning());
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                if (firstError.compareAndSet(null, new PineErrors.OperatorException("parallel execution interrupted", e))) {
                    shardCtx.cancel();
                }
            } catch (ExecutionException e) {
                Exception cause = e.getCause() instanceof Exception
                        ? (Exception) e.getCause()
                        : new PineErrors.PanicError("parallel-shard", e.getCause());
                if (firstError.compareAndSet(null, cause)) {
                    shardCtx.cancel();
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
        if (skipped.get()) {
            throw shardCtx.err();
        }
        return merged;
    }
}
