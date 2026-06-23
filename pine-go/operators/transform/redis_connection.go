// Resource: redis_connection
// Description: Shared Redis connection pool. Created once and held for the
// lifetime of the ResourceManager (never refreshed). Operators such as
// transform_redis_get / transform_redis_set reference it by resource_name and
// borrow the *redis.Client per request, so multiple operators pointing at the
// same connection resource share a single pool. The pool is closed when the
// ResourceManager retires and the last in-flight borrow is released.
//
// Params:
//   - addr (string, required): Redis server address (host:port).
//   - password (string, optional, default=""): Redis password.
//   - db (int, optional, default=0): Redis DB number.
//   - dial_timeout_ms (int, optional, default=2000): TCP dial timeout in ms.
//   - read_timeout_ms (int, optional, default=2000): Per-command read timeout
//     in ms. This is the dominant cascade-safety knob: any single Redis call
//     that exceeds this deadline returns ctx.DeadlineExceeded so callers don't
//     hold the borrowed client (or upstream goroutine) indefinitely while the
//     server is slow. Most operators (redis_get/set/zrangebyscore) default
//     fail_on_error=false and degrade to a cache miss + warning, so a slow
//     Redis is contained at this resource boundary instead of cascading into
//     the request goroutine pool.
//   - write_timeout_ms (int, optional, default=2000): Per-command write
//     timeout in ms. pine-java honours this as max(read_timeout_ms,
//     write_timeout_ms) due to Jedis's single socket timeout; pine-go and
//     pine-cpp honour read/write independently.
//   - pool_timeout_ms (int, optional, default=2000): How long a goroutine
//     waits for a free connection from the pool when all connections are in
//     use. Bounds the upstream-induced pile-up when Redis stops returning.
//   - pool_size (int, optional, default=0): Maximum concurrent connections
//     in the pool. 0 leaves the redis client default in place
//     (10*GOMAXPROCS for go-redis v9), which has been the de-facto behaviour;
//     set explicitly to lift the cap for high-concurrency deployments.
//   - metrics_name (string, optional, default=""): When set, the pool emits its
//     own metrics (pool gauges + PING-probe latency) labelled name=<metrics_name>.
//     Empty disables resource-level metrics.
package transform

import (
	"context"
	"fmt"
	"sync"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
	"github.com/redis/go-redis/v9"
)

// redisProbeInterval is how often the background probe samples pool stats and
// pings the server. Fixed across runtimes so metric cadence is comparable.
const redisProbeInterval = 15 * time.Second

// Default cascade-safety timeouts. 2s is below go-redis v9's defaults
// (read/write 3s, dial 5s) but high enough not to break healthy operations:
// a tipsy-recsys 2026-06-22 incident showed Redis PING p99 latency spiking
// to ~970ms and dragging /execute through 10s timeouts because the resource
// inherited the v9 defaults verbatim. Production deployments should configure
// tighter values (e.g. 500ms read/write) when their workload allows.
const (
	defaultRedisDialTimeoutMs  = 2000
	defaultRedisReadTimeoutMs  = 2000
	defaultRedisWriteTimeoutMs = 2000
	defaultRedisPoolTimeoutMs  = 2000
)

func init() {
	pine.RegisterResource(pine.ResourceSchema{
		Name:            "redis_connection",
		Description:     "Shared Redis connection pool borrowed by Redis operators via resource_name.",
		DefaultInterval: -1, // never refresh: a connection pool has no meaningful refresh.
		Params: map[string]pine.ParamSpec{
			"addr":             {Type: "string", Required: true, Description: "Redis server address (host:port)."},
			"password":         {Type: "string", Required: false, Default: "", Description: "Redis password."},
			"db":               {Type: "int", Required: false, Default: 0, Description: "Redis DB number."},
			"dial_timeout_ms":  {Type: "int", Required: false, Default: defaultRedisDialTimeoutMs, Description: "TCP dial timeout in ms."},
			"read_timeout_ms":  {Type: "int", Required: false, Default: defaultRedisReadTimeoutMs, Description: "Per-command read timeout in ms; primary cascade-safety knob."},
			"write_timeout_ms": {Type: "int", Required: false, Default: defaultRedisWriteTimeoutMs, Description: "Per-command write timeout in ms. pine-java note: Jedis exposes a single socket timeout, so the effective deadline on this engine is max(read_timeout_ms, write_timeout_ms); keep read_timeout_ms >= write_timeout_ms to avoid surprise. pine-go and pine-cpp honour read/write independently."},
			"pool_timeout_ms":  {Type: "int", Required: false, Default: defaultRedisPoolTimeoutMs, Description: "How long a goroutine waits for a free pool connection in ms."},
			"pool_size":        {Type: "int", Required: false, Default: 0, Description: "Maximum concurrent connections; 0 = client default (10*GOMAXPROCS)."},
			"metrics_name":     {Type: "string", Required: false, Default: "", Description: "When set, the pool emits its own metrics labelled name=<metrics_name>. Empty disables resource-level metrics."},
		},
	}, func(params map[string]any, mp metrics.Provider) (resource.Fetcher, error) {
		opts, metricsName, err := redisOptionsFromParams(params)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context) (any, error) {
			client := redis.NewClient(opts)
			// The wrapper implements io.Closer; the ResourceManager closes it
			// on retirement once the last borrow is released, which stops the
			// probe and closes the underlying client.
			return newRedisConnResource(client, metricsName, mp), nil
		}, nil
	})
}

// redisOptionsFromParams translates the registered ParamSpec map into a
// *redis.Options + metrics_name pair. Split out from the schema closure so
// tests can verify that user params (especially the cascade-safety timeouts)
// are wired through to the client without spinning up miniredis.
func redisOptionsFromParams(params map[string]any) (*redis.Options, string, error) {
	addr, _ := params["addr"].(string)
	if addr == "" {
		return nil, "", fmt.Errorf("redis_connection: addr is required")
	}
	password, _ := params["password"].(string)
	db := 0
	if v, ok := params["db"]; ok {
		db = int(toInt64Param(v))
	}
	dialTimeout := time.Duration(intParamOrDefault(params, "dial_timeout_ms", defaultRedisDialTimeoutMs)) * time.Millisecond
	readTimeout := time.Duration(intParamOrDefault(params, "read_timeout_ms", defaultRedisReadTimeoutMs)) * time.Millisecond
	writeTimeout := time.Duration(intParamOrDefault(params, "write_timeout_ms", defaultRedisWriteTimeoutMs)) * time.Millisecond
	poolTimeout := time.Duration(intParamOrDefault(params, "pool_timeout_ms", defaultRedisPoolTimeoutMs)) * time.Millisecond
	poolSize := int(intParamOrDefault(params, "pool_size", 0))
	metricsName, _ := params["metrics_name"].(string)
	return &redis.Options{
		Addr:         addr,
		Password:     password,
		DB:           db,
		DialTimeout:  dialTimeout,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		PoolTimeout:  poolTimeout,
		PoolSize:     poolSize,
	}, metricsName, nil
}

// intParamOrDefault reads an int-like param with a fallback when the key is
// absent. Callers normally set Default in ParamSpec; this helper keeps the
// factory body readable when several optional ints share the same shape.
func intParamOrDefault(params map[string]any, key string, fallback int64) int64 {
	if v, ok := params[key]; ok {
		return toInt64Param(v)
	}
	return fallback
}

// RedisConnResource wraps a *redis.Client borrowed by Redis operators via
// resource_name. When constructed with a non-empty metrics name it runs a
// background probe that samples pool stats and PING latency until Close.
type RedisConnResource struct {
	client *redis.Client

	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

// newRedisConnResource builds the wrapper. When metricsName is empty (or the
// provider is nil) no metrics are emitted and no probe goroutine is started.
func newRedisConnResource(client *redis.Client, metricsName string, mp metrics.Provider) *RedisConnResource {
	r := &RedisConnResource{client: client}
	if metricsName == "" || mp == nil {
		return r
	}
	totalConns := mp.NewGauge(metrics.MetricOpts{
		Name: "pine_redis_pool_total_conns", Help: "Total Redis connections in the pool (idle + in-use).", LabelNames: []string{"name"},
	}).With(metricsName)
	idleConns := mp.NewGauge(metrics.MetricOpts{
		Name: "pine_redis_pool_idle_conns", Help: "Idle Redis connections in the pool.", LabelNames: []string{"name"},
	}).With(metricsName)
	pingDuration := mp.NewHistogram(metrics.HistogramOpts{
		MetricOpts: metrics.MetricOpts{
			Name: "pine_redis_ping_duration_seconds", Help: "Redis PING probe latency in seconds.", LabelNames: []string{"name"},
		},
	}).With(metricsName)
	up := mp.NewGauge(metrics.MetricOpts{
		Name: "pine_redis_up", Help: "Whether the last Redis PING probe succeeded (1) or failed (0).", LabelNames: []string{"name"},
	}).With(metricsName)

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.wg.Add(1)
	go r.probeLoop(ctx, totalConns, idleConns, pingDuration, up)
	return r
}

// Client returns the borrowed Redis client. Valid only while the borrow handle
// is held.
func (r *RedisConnResource) Client() *redis.Client { return r.client }

// probeLoop samples pool stats and PING latency until the context is cancelled.
// It runs one probe immediately so metrics are populated before the first tick.
func (r *RedisConnResource) probeLoop(ctx context.Context, totalConns, idleConns metrics.Gauge, pingDuration metrics.Histogram, up metrics.Gauge) {
	defer r.wg.Done()
	probe := func() {
		stats := r.client.PoolStats()
		totalConns.Set(float64(stats.TotalConns))
		idleConns.Set(float64(stats.IdleConns))
		start := time.Now()
		err := r.client.Ping(ctx).Err()
		pingDuration.Observe(metrics.DurationSeconds(time.Since(start)))
		if err != nil {
			up.Set(0)
		} else {
			up.Set(1)
		}
	}
	probe()
	ticker := time.NewTicker(redisProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			probe()
		}
	}
}

// Close stops the background probe (if any) and closes the underlying client.
// Safe to call multiple times.
func (r *RedisConnResource) Close() error {
	var err error
	r.once.Do(func() {
		if r.cancel != nil {
			r.cancel()
			r.wg.Wait()
		}
		err = r.client.Close()
	})
	return err
}
