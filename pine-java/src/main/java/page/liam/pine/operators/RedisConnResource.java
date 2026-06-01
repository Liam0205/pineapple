package page.liam.pine.operators;

import page.liam.pine.metrics.Gauge;
import page.liam.pine.metrics.Histogram;
import page.liam.pine.metrics.HistogramOpts;
import page.liam.pine.metrics.MetricOpts;
import page.liam.pine.metrics.Provider;
import redis.clients.jedis.Jedis;
import redis.clients.jedis.JedisPool;

import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;

/**
 * Wraps a {@link JedisPool} borrowed by Redis operators via resource_name.
 * When constructed with a non-empty metrics name it runs a background probe
 * that samples pool stats and PING latency until {@link #close()}, mirroring
 * pine-go's RedisConnResource. The metric names, labels and probe cadence match
 * the Go reference byte-for-byte.
 */
public final class RedisConnResource implements AutoCloseable {

    /** How often the probe samples pool stats and pings the server (parity with Go). */
    static final long PROBE_INTERVAL_SECONDS = 15;

    private final JedisPool pool;
    private ScheduledExecutorService probe;

    public RedisConnResource(JedisPool pool, String metricsName, Provider metrics) {
        this.pool = pool;
        if (metricsName == null || metricsName.isEmpty() || metrics == null) {
            return;
        }
        Gauge totalConns = metrics.newGauge(new MetricOpts(
                "pine_redis_pool_total_conns",
                "Total Redis connections in the pool (idle + in-use).", "name")).with(metricsName);
        Gauge idleConns = metrics.newGauge(new MetricOpts(
                "pine_redis_pool_idle_conns",
                "Idle Redis connections in the pool.", "name")).with(metricsName);
        Histogram pingDuration = metrics.newHistogram(new HistogramOpts(
                "pine_redis_ping_duration_seconds",
                "Redis PING probe latency in seconds.", null, "name")).with(metricsName);
        Gauge up = metrics.newGauge(new MetricOpts(
                "pine_redis_up",
                "Whether the last Redis PING probe succeeded (1) or failed (0).", "name")).with(metricsName);

        probe = Executors.newScheduledThreadPool(1, runnable -> {
            Thread t = new Thread(runnable, "redis-probe");
            t.setDaemon(true);
            return t;
        });
        Runnable sample = () -> probeOnce(totalConns, idleConns, pingDuration, up);
        sample.run(); // populate metrics before the first scheduled tick
        probe.scheduleAtFixedRate(sample, PROBE_INTERVAL_SECONDS, PROBE_INTERVAL_SECONDS, TimeUnit.SECONDS);
    }

    /** Returns the borrowed pool. Valid only while the resource is held. */
    public JedisPool pool() {
        return pool;
    }

    private void probeOnce(Gauge totalConns, Gauge idleConns, Histogram pingDuration, Gauge up) {
        totalConns.set(pool.getNumActive() + pool.getNumIdle());
        idleConns.set(pool.getNumIdle());
        long start = System.nanoTime();
        boolean ok = true;
        try (Jedis jedis = pool.getResource()) {
            jedis.ping();
        } catch (Exception e) {
            ok = false;
        }
        pingDuration.observe((System.nanoTime() - start) / 1e9);
        up.set(ok ? 1 : 0);
    }

    /** Stops the background probe (if any) and closes the underlying pool. */
    @Override
    public synchronized void close() {
        if (probe != null) {
            probe.shutdownNow();
            try {
                probe.awaitTermination(5, TimeUnit.SECONDS);
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }
            probe = null;
        }
        pool.close();
    }
}
