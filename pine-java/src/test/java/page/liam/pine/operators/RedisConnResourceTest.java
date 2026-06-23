package page.liam.pine.operators;

import org.junit.jupiter.api.Test;
import page.liam.pine.Codegen;
import page.liam.pine.ResourceRegistry;
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

    /**
     * The cascade-safety timeouts and pool_size knob must appear in the registered
     * schema with the cross-engine-shared default values. Locked here as the
     * regression test for the 2026-06-22 tipsy-recsys outage: the resource
     * inherited Jedis defaults (single hard-coded 2s connect timeout, no socket
     * timeout) and a brief Redis hiccup escalated into a 30-minute /execute
     * outage. Mirrors pine-go's TestRedisOptionsFromParams_Defaults.
     */
    @Test
    void schemaExposesCascadeSafetyParams() {
        AllOperators.ensureRegistered();
        Codegen.ResourceSchema schema = null;
        for (Codegen.ResourceSchema s : ResourceRegistry.all()) {
            if ("redis_connection".equals(s.name)) {
                schema = s;
                break;
            }
        }
        assertNotNull(schema, "redis_connection schema should be registered");

        assertEquals(2000L, ((Number) schema.params.get("dial_timeout_ms").defaultValue).longValue());
        assertEquals(2000L, ((Number) schema.params.get("read_timeout_ms").defaultValue).longValue());
        assertEquals(2000L, ((Number) schema.params.get("write_timeout_ms").defaultValue).longValue());
        assertEquals(2000L, ((Number) schema.params.get("pool_timeout_ms").defaultValue).longValue());
        assertEquals(0L, ((Number) schema.params.get("pool_size").defaultValue).longValue());

        // Required-field guard remains intact.
        assertTrue(schema.params.get("addr").required);
        assertFalse(schema.params.get("read_timeout_ms").required);
    }

    /**
     * Pin the Jedis-specific mapping: pine-java's single socketTimeoutMillis is
     * driven by max(read, write). The 2026-06-22 review (.code-review/from-
     * 24975c2/...) flagged that a later switch to min would silently turn long-
     * running reads (LRANGE / ZRANGEBYSCORE) into write failures, defeating the
     * cascade-safety story for those commands. Lock the policy and the schema
     * description so the two stay in sync.
     */
    @Test
    void writeTimeoutMapsToMaxOfReadAndWriteOnJedis() {
        // Symmetric configuration: both directions get the same wall.
        assertEquals(500, AllOperators.effectiveSocketTimeoutMs(500, 500));

        // Asymmetric — write higher than read. Documents the real choice:
        // the larger value wins so reads that legitimately wait on the server
        // (LRANGE etc.) aren't capped at the write deadline.
        assertEquals(2000, AllOperators.effectiveSocketTimeoutMs(500, 2000));
        assertEquals(2000, AllOperators.effectiveSocketTimeoutMs(2000, 500));

        // Defaults from the schema (2000 / 2000) yield 2000 verbatim.
        assertEquals(2000, AllOperators.effectiveSocketTimeoutMs(2000, 2000));
    }

    /**
     * The schema description for write_timeout_ms must surface the Java-
     * specific max() behaviour so users configuring asymmetric timeouts on
     * Apple's Python DSL learn the engine difference at config time, not
     * after a production incident. Locks the language so a future drift on
     * the description (and thus the codegen-emitted docstring) catches the
     * drift on the policy at the same time.
     */
    @Test
    void writeTimeoutSchemaDescriptionMentionsMaxBehaviour() {
        AllOperators.ensureRegistered();
        Codegen.ResourceSchema schema = null;
        for (Codegen.ResourceSchema s : ResourceRegistry.all()) {
            if ("redis_connection".equals(s.name)) {
                schema = s;
                break;
            }
        }
        assertNotNull(schema);
        String desc = schema.params.get("write_timeout_ms").description;
        assertNotNull(desc);
        assertTrue(desc.contains("max(read_timeout_ms, write_timeout_ms)"),
                "write_timeout_ms description must mention max() behaviour, got: " + desc);
    }
}
