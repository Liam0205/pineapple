package page.liam.pine.operators;

import org.junit.jupiter.api.Test;
import page.liam.pine.metrics.Counter;
import page.liam.pine.metrics.Gauge;
import page.liam.pine.metrics.Histogram;
import page.liam.pine.metrics.HistogramOpts;
import page.liam.pine.metrics.MetricOpts;
import page.liam.pine.metrics.NopProvider;
import page.liam.pine.metrics.Provider;
import redis.clients.jedis.JedisPool;
import redis.clients.jedis.JedisPoolConfig;

import java.util.ArrayList;
import java.util.List;

import static org.junit.jupiter.api.Assertions.*;

/**
 * Verifies the metrics gate on the redis_connection resource wrapper, mirroring
 * pine-go's TestRedisConnResource_MetricsGate_*. JedisPool construction is lazy,
 * so these tests need no real Redis server: the gate decision and metric
 * registration happen at wrapper construction time.
 */
public class RedisConnResourceTest {

    private static final List<String> REDIS_METRICS = List.of(
            "pine_redis_pool_total_conns",
            "pine_redis_pool_idle_conns",
            "pine_redis_ping_duration_seconds",
            "pine_redis_up");

    /** A Provider that records the names of every metric created through it. */
    private static final class RecordingProvider implements Provider {
        final List<String> names = new ArrayList<>();

        @Override
        public Counter newCounter(MetricOpts opts) {
            names.add(opts.name);
            return NopProvider.getInstance().newCounter(opts);
        }

        @Override
        public Gauge newGauge(MetricOpts opts) {
            names.add(opts.name);
            return NopProvider.getInstance().newGauge(opts);
        }

        @Override
        public Histogram newHistogram(HistogramOpts opts) {
            names.add(opts.name);
            return NopProvider.getInstance().newHistogram(opts);
        }
    }

    private static JedisPool lazyPool() {
        // Points at a non-listening port; construction does not connect.
        return new JedisPool(new JedisPoolConfig(), "127.0.0.1", 1);
    }

    @Test
    void metricsGateDisabledByEmptyName() {
        RecordingProvider rec = new RecordingProvider();
        try (RedisConnResource r = new RedisConnResource(lazyPool(), "", rec)) {
            for (String name : REDIS_METRICS) {
                assertFalse(rec.names.contains(name),
                        "metric " + name + " created when metrics_name is empty");
            }
        }
    }

    @Test
    void metricsGateEnabledByName() {
        RecordingProvider rec = new RecordingProvider();
        try (RedisConnResource r = new RedisConnResource(lazyPool(), "cache", rec)) {
            for (String name : REDIS_METRICS) {
                assertTrue(rec.names.contains(name),
                        "metric " + name + " not created when metrics_name is set");
            }
        }
    }

    @Test
    void closeIsIdempotent() {
        RedisConnResource r = new RedisConnResource(lazyPool(), "cache", new RecordingProvider());
        r.close();
        r.close(); // must not throw
    }
}
