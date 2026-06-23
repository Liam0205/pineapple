package page.liam.pine.operators;

import page.liam.pine.metrics.Counter;
import page.liam.pine.metrics.Gauge;
import page.liam.pine.metrics.Histogram;
import page.liam.pine.metrics.HistogramOpts;
import page.liam.pine.metrics.MetricOpts;
import page.liam.pine.metrics.Provider;
import redis.clients.jedis.Jedis;
import redis.clients.jedis.JedisPool;
import redis.clients.jedis.exceptions.JedisConnectionException;
import redis.clients.jedis.exceptions.JedisDataException;
import redis.clients.jedis.exceptions.JedisException;

import java.net.SocketTimeoutException;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.function.Function;

/**
 * Wraps a {@link JedisPool} borrowed by Redis operators via resource_name.
 * When constructed with a non-empty metrics name it runs a background probe
 * that samples pool stats and PING latency until {@link #close()}, mirroring
 * pine-go's RedisConnResource. The metric names, labels and probe cadence match
 * the Go reference byte-for-byte.
 *
 * <p>Operators wrap each Redis call in {@link #runCommand(String, Function)}
 * to get the per-command observability metrics (latency histogram + status
 * counter) for free. pine-go gets the same coverage via go-redis's hook
 * interface; Jedis has no equivalent, so the resource exposes a callable
 * facade instead.
 */
public final class RedisConnResource implements AutoCloseable {

    /** How often the probe samples pool stats and pings the server (parity with Go). */
    static final long PROBE_INTERVAL_SECONDS = 15;

    private final JedisPool pool;
    private ScheduledExecutorService probe;
    // Per-command metrics. Null when metrics_name is empty (no observability
    // requested), in which case runCommand bypasses recording entirely so the
    // hot path stays a plain pool.getResource + lambda.
    private final String metricsName;
    private final Histogram cmdDuration;
    private final Counter cmdTotal;

    public RedisConnResource(JedisPool pool, String metricsName, Provider metrics) {
        this.pool = pool;
        if (metricsName == null || metricsName.isEmpty() || metrics == null) {
            this.metricsName = null;
            this.cmdDuration = null;
            this.cmdTotal = null;
            return;
        }
        this.metricsName = metricsName;
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
        // Per-command observability — held unbound; runCommand applies the
        // (name, command, status) tuple per call. The 2026-06-22 recsys outage
        // (.code-review/from-24975c2/...) had no signal here, only PING probe
        // (sampled every 15s) and pool gauges; the status taxonomy on these
        // labels distinguishes "Redis is slow" from "we ran out of pool".
        this.cmdDuration = metrics.newHistogram(new HistogramOpts(
                "pine_redis_command_duration_seconds",
                "Redis command latency in seconds, labelled by command name and outcome.",
                null, "name", "command", "status"));
        this.cmdTotal = metrics.newCounter(new MetricOpts(
                "pine_redis_command_total",
                "Cumulative count of Redis command invocations, labelled by command name and outcome.",
                "name", "command", "status"));

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

    /**
     * Borrow a {@link Jedis} from the pool, run the given action, and record a
     * latency observation + status-tagged counter increment. Wraps the
     * try-with-resources so callers can't accidentally bypass the metric.
     *
     * <p>Exceptions are re-thrown after observation so the operator-level
     * error handling (fail_on_error, setWarning) keeps working. If a deployment
     * opts out of resource metrics by leaving {@code metrics_name} empty, this
     * is a thin pass-through with no recording.
     *
     * @param command Command name in upper-case ({@code "GET"}, {@code "SET"},
     *                {@code "ZRANGEBYSCORE"} ...). Becomes the {@code command}
     *                label on the resulting metrics.
     * @param action  The Jedis call to time. Receives a borrowed Jedis;
     *                must not retain it past the lambda.
     */
    public <T> T runCommand(String command, Function<Jedis, T> action) {
        if (cmdTotal == null) {
            try (Jedis jedis = pool.getResource()) {
                return action.apply(jedis);
            }
        }
        long start = System.nanoTime();
        try (Jedis jedis = pool.getResource()) {
            T result = action.apply(jedis);
            recordCommand(command, System.nanoTime() - start, null);
            return result;
        } catch (RuntimeException e) {
            recordCommand(command, System.nanoTime() - start, e);
            throw e;
        }
    }

    private void recordCommand(String command, long elapsedNanos, Throwable err) {
        String status = redisCommandStatus(err);
        double seconds = elapsedNanos / 1e9;
        cmdDuration.with(metricsName, command, status).observe(seconds);
        cmdTotal.with(metricsName, command, status).inc();
    }

    /**
     * Classify a Jedis exception into the status label values used on the
     * per-command metrics. Mirrors pine-go's redisCommandStatus taxonomy
     * verbatim so a Grafana panel querying e.g.
     * {@code rate(pine_redis_command_total{status="timeout"}[1m])} renders
     * the same shape regardless of which engine produced the data.
     *
     * <p>Status values:
     * <ul>
     *   <li>{@code ok} — call returned normally (Jedis returns null on cache
     *       miss without throwing, so there's no "not-found" exception to
     *       catch here).</li>
     *   <li>{@code timeout} — {@link SocketTimeoutException} chain;
     *       Jedis surfaces socket-timeout as JedisConnectionException with
     *       a SocketTimeoutException cause.</li>
     *   <li>{@code pool_timeout} — JedisException whose message indicates
     *       pool exhaustion; commons-pool2 wraps the failure as
     *       JedisException("Could not get a resource ...").</li>
     *   <li>{@code error} — anything else (data error, AUTH, protocol).</li>
     * </ul>
     */
    static String redisCommandStatus(Throwable err) {
        if (err == null) {
            return "ok";
        }
        // Pool exhaustion: Jedis's pool layer wraps commons-pool2's
        // NoSuchElementException as JedisException with a recognisable msg.
        if (err instanceof JedisException) {
            String msg = err.getMessage();
            if (msg != null && (msg.contains("Could not get a resource") || msg.contains("pool exhausted"))) {
                return "pool_timeout";
            }
        }
        // Walk the cause chain looking for a SocketTimeoutException; this is
        // how Jedis surfaces command-level read/connect timeouts.
        for (Throwable t = err; t != null; t = t.getCause()) {
            if (t instanceof SocketTimeoutException) {
                return "timeout";
            }
            if (t == t.getCause()) {
                break; // self-loop guard
            }
        }
        // Connection-level failures and data errors fall through.
        if (err instanceof JedisConnectionException || err instanceof JedisDataException) {
            return "error";
        }
        return "error";
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
