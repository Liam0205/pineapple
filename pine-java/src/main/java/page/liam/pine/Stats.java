package page.liam.pine;

import java.util.*;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

public class Stats {
    private final ConcurrentHashMap<String, OpStats> ops = new ConcurrentHashMap<>();
    private final AtomicLong runCount = new AtomicLong();
    private final AtomicLong peakConcurrency = new AtomicLong();

    public void preInitOperators(List<String> names) {
        for (String name : names) {
            ops.putIfAbsent(name, new OpStats());
        }
    }

    public void recordExec(String name, long durationNs) {
        getOrCreate(name).recordExec(durationNs);
    }

    public void recordSkip(String name) {
        getOrCreate(name).skipCount.incrementAndGet();
    }

    public void recordError(String name, long durationNs) {
        getOrCreate(name).recordError(durationNs);
    }

    public void recordRun() {
        runCount.incrementAndGet();
    }

    public void recordConcurrency(long n) {
        long prev;
        do {
            prev = peakConcurrency.get();
            if (n <= prev) return;
        } while (!peakConcurrency.compareAndSet(prev, n));
    }

    public Map<String, Map<String, Object>> snapshot() {
        Map<String, Map<String, Object>> result = new TreeMap<>();
        for (Map.Entry<String, OpStats> e : ops.entrySet()) {
            result.put(e.getKey(), e.getValue().snapshot());
        }
        return result;
    }

    public Map<String, Object> schedulerSnapshot() {
        Map<String, Object> m = new LinkedHashMap<>();
        m.put("run_count", runCount.get());
        m.put("peak_concurrency", peakConcurrency.get());
        return m;
    }

    private OpStats getOrCreate(String name) {
        return ops.computeIfAbsent(name, k -> new OpStats());
    }

    public static class OpStats {
        final AtomicLong execCount = new AtomicLong();
        final AtomicLong skipCount = new AtomicLong();
        final AtomicLong errorCount = new AtomicLong();
        final AtomicLong totalDurationNs = new AtomicLong();
        final AtomicLong maxDurationNs = new AtomicLong();

        void recordExec(long durationNs) {
            execCount.incrementAndGet();
            totalDurationNs.addAndGet(durationNs);
            long prev;
            do {
                prev = maxDurationNs.get();
                if (durationNs <= prev) return;
            } while (!maxDurationNs.compareAndSet(prev, durationNs));
        }

        void recordError(long durationNs) {
            errorCount.incrementAndGet();
            totalDurationNs.addAndGet(durationNs);
            long prev;
            do {
                prev = maxDurationNs.get();
                if (durationNs <= prev) return;
            } while (!maxDurationNs.compareAndSet(prev, durationNs));
        }

        Map<String, Object> snapshot() {
            long exec = execCount.get();
            long total = totalDurationNs.get();
            Map<String, Object> m = new LinkedHashMap<>();
            m.put("exec_count", exec);
            m.put("skip_count", skipCount.get());
            m.put("error_count", errorCount.get());
            m.put("total_duration_ns", total);
            m.put("max_duration_ns", maxDurationNs.get());
            m.put("avg_duration_ns", exec > 0 ? total / exec : 0L);
            return m;
        }
    }
}
