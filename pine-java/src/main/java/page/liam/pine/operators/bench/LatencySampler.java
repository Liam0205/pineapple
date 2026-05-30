package page.liam.pine.operators.bench;

import java.util.List;
import java.util.Map;
import java.util.concurrent.ThreadLocalRandom;

class LatencySampler {
    private final double p50Mean;
    private final double p50Max;
    private final double p99Mean;
    private final double p99Max;
    private final boolean isIO;
    private final long iterations; // calibrated mode: base iteration count for p50Mean

    LatencySampler(double p50Mean, double p50Max, double p99Mean, double p99Max, boolean isIO, long iterations) {
        this.p50Mean = p50Mean;
        this.p50Max = p50Max;
        this.p99Mean = p99Mean;
        this.p99Max = p99Max;
        this.isIO = isIO;
        this.iterations = iterations;
    }

    double apply() {
        long micros = sample();
        if (micros <= 0) return 0.0;
        if (isIO) {
            try {
                Thread.sleep(micros / 1000, (int) ((micros % 1000) * 1000));
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            return 0.0;
        }
        if (iterations > 0) {
            // Calibrated mode: scale iterations proportionally to sampled duration
            double targetMicros = p50Mean * 1000.0;
            double ratio = micros / targetMicros;
            long n = Math.max(1, (long) (iterations * ratio));
            return cpuWork(n);
        }
        // Time-based mode: compute until timeout
        long deadline = System.nanoTime() + micros * 1000L;
        double acc = 1.0;
        ThreadLocalRandom rng = ThreadLocalRandom.current();
        while (System.nanoTime() < deadline) {
            double a = rng.nextDouble() * 1000.0 + 1.0;
            double b = rng.nextDouble() * 1000.0 + 1.0;
            acc += a / b;
            a = rng.nextDouble() * 1000.0 + 1.0;
            b = rng.nextDouble() * 1000.0 + 1.0;
            acc -= a / b;
        }
        return acc;
    }

    private static double cpuWork(long n) {
        ThreadLocalRandom rng = ThreadLocalRandom.current();
        double acc = 1.0;
        for (long i = 0; i < n; i++) {
            double a = rng.nextDouble() * 1000.0 + 1.0;
            double b = rng.nextDouble() * 1000.0 + 1.0;
            acc += a / b;
            a = rng.nextDouble() * 1000.0 + 1.0;
            b = rng.nextDouble() * 1000.0 + 1.0;
            acc -= a / b;
        }
        return acc;
    }

    private long sample() {
        if (p50Mean <= 0 && p99Mean <= 0) return 0;

        double jitter = ThreadLocalRandom.current().nextDouble();
        double p50 = p50Mean + jitter * (p50Max - p50Mean);
        double p99 = p99Mean + jitter * (p99Max - p99Mean);

        if (p50 <= 0) p50 = 0.001;
        if (p99 <= p50) p99 = p50 * 2;

        double mu = Math.log(p50);
        double sigma = (Math.log(p99) - mu) / 2.326;
        if (sigma <= 0) sigma = 0.1;

        double s = Math.exp(mu + sigma * ThreadLocalRandom.current().nextGaussian());

        double cap = p99 * 1.5;
        if (s > cap) s = cap;
        if (s < 0) s = 0;

        return (long) (s * 1000.0);
    }

    @SuppressWarnings("unchecked")
    static LatencySampler parse(Map<String, Object> params) {
        Object raw = params.get("bench_profile");
        if (raw == null || !(raw instanceof Map)) return null;
        Map<String, Object> m = (Map<String, Object>) raw;

        double p50Mean = 0, p50Max = 0, p99Mean = 0, p99Max = 0;
        boolean isIO = false;
        long iterations = 0;

        Object p50 = m.get("p50");
        if (p50 instanceof List) {
            List<?> arr = (List<?>) p50;
            if (arr.size() >= 2) {
                p50Mean = toDouble(arr.get(0));
                p50Max = toDouble(arr.get(1));
            }
        }
        Object p99 = m.get("p99");
        if (p99 instanceof List) {
            List<?> arr = (List<?>) p99;
            if (arr.size() >= 2) {
                p99Mean = toDouble(arr.get(0));
                p99Max = toDouble(arr.get(1));
            }
        }
        Object type = m.get("type");
        if (type instanceof String) {
            isIO = "io".equals(type);
        }
        Object iter = m.get("iterations");
        if (iter != null) {
            iterations = (long) toDouble(iter);
        }

        if (p50Mean <= 0 && p99Mean <= 0) return null;

        return new LatencySampler(p50Mean, p50Max, p99Mean, p99Max, isIO, iterations);
    }

    private static double toDouble(Object v) {
        if (v instanceof Number) return ((Number) v).doubleValue();
        return 0;
    }
}