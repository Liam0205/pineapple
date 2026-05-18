package page.liam.pine;

import java.util.*;
import java.util.concurrent.*;

public class ParallelExecutor {

    private ParallelExecutor() {}

    public static OperatorOutput execute(Operator op, OperatorInput input, int parallelism) throws Exception {
        int total = input.itemCount();
        if (parallelism <= 1 || total == 0) {
            OperatorOutput output = new OperatorOutput();
            op.execute(input, output);
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
            op.execute(shards.get(0), output);
            return output;
        }

        ExecutorService executor = ForkJoinPool.commonPool();
        List<Future<OperatorOutput>> futures = new ArrayList<>(n);

        for (OperatorInput shard : shards) {
            futures.add(executor.submit(() -> {
                OperatorOutput out = new OperatorOutput();
                op.execute(shard, out);
                return out;
            }));
        }

        OperatorOutput merged = new OperatorOutput();
        Exception firstError = null;

        for (int i = 0; i < futures.size(); i++) {
            try {
                OperatorOutput out = futures.get(i).get();
                int offset = offsets[i];
                for (Map.Entry<Integer, Map<String, Object>> entry : out.getItemWrites().entrySet()) {
                    int absIdx = entry.getKey() + offset;
                    for (Map.Entry<String, Object> field : entry.getValue().entrySet()) {
                        merged.setItem(absIdx, field.getKey(), field.getValue());
                    }
                }
            } catch (ExecutionException e) {
                if (firstError == null) {
                    firstError = e.getCause() instanceof Exception
                            ? (Exception) e.getCause() : new Exception(e.getCause());
                }
            }
        }

        if (firstError != null) {
            throw firstError;
        }
        return merged;
    }
}
