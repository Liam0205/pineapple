package page.liam.pine;

import java.util.LinkedHashMap;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Per-process atomic accumulators for HTTP request observability. Mirrors
 * pine-go {@code pkg/server.HttpStats}: the HTTP metrics middleware writes
 * both an external Provider (Counter/Histogram) and this in-memory
 * structure, so {@code /stats} can expose request counts and duration
 * sums without requiring a Prometheus adapter.
 *
 * Keys are byte-exact with the Go reference:
 *   requests_total: "{@literal <METHOD> <path> <statusBucket>}"
 *   request_duration_seconds: "{@literal <METHOD> <path>}"
 */
public final class HttpStats {
    private final ConcurrentHashMap<String, AtomicLong> requests = new ConcurrentHashMap<>();
    private final ConcurrentHashMap<String, DurationBucket> durations = new ConcurrentHashMap<>();

    public void recordRequest(String method, String path, String statusBucket, long durationNs) {
        String reqKey = method + " " + path + " " + statusBucket;
        requests.computeIfAbsent(reqKey, k -> new AtomicLong()).incrementAndGet();

        String durKey = method + " " + path;
        DurationBucket bucket = durations.computeIfAbsent(durKey, k -> new DurationBucket());
        bucket.count.incrementAndGet();
        bucket.sumNs.addAndGet(durationNs);
    }

    /**
     * Returns a deterministic snapshot. Outer maps are TreeMap so JSON
     * encoders that preserve insertion order (Jackson default) emit keys in
     * lexicographic ascending order, matching Go's encoding/json behavior.
     * Inner duration buckets emit {@code count} then {@code sum_ns}.
     */
    public Map<String, Object> snapshot() {
        TreeMap<String, Long> reqOut = new TreeMap<>();
        for (Map.Entry<String, AtomicLong> e : requests.entrySet()) {
            reqOut.put(e.getKey(), e.getValue().get());
        }
        TreeMap<String, Map<String, Long>> durOut = new TreeMap<>();
        for (Map.Entry<String, DurationBucket> e : durations.entrySet()) {
            DurationBucket b = e.getValue();
            Map<String, Long> bucketView = new LinkedHashMap<>();
            bucketView.put("count", b.count.get());
            bucketView.put("sum_ns", b.sumNs.get());
            durOut.put(e.getKey(), bucketView);
        }
        Map<String, Object> result = new LinkedHashMap<>();
        result.put("request_duration_seconds", durOut);
        result.put("requests_total", reqOut);
        return result;
    }

    private static final class DurationBucket {
        final AtomicLong count = new AtomicLong();
        final AtomicLong sumNs = new AtomicLong();
    }
}
