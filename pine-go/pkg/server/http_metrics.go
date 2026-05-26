package server

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
)

var knownPaths = map[string]bool{
	"/execute": true,
	"/health":  true,
	"/stats":   true,
	"/dag":     true,
}

func normalizePath(path string) string {
	if knownPaths[path] {
		return path
	}
	return "_other"
}

func statusBucket(code int) string {
	switch {
	case code >= 500:
		return "5xx"
	case code >= 400:
		return "4xx"
	case code >= 300:
		return "3xx"
	case code >= 200:
		return "2xx"
	default:
		return "other"
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// HttpDurationBucket aggregates count and total duration (in nanoseconds)
// for a method+path key. Mirrors the operators.total_duration_ns convention.
type HttpDurationBucket struct {
	Count  int64 `json:"count"`
	SumNs  int64 `json:"sum_ns"`
}

// HttpStats accumulates per-(method,path,status) request counts and
// per-(method,path) duration totals. Always-on inside the HTTP metrics
// middleware so /stats can expose http observability without requiring an
// external Provider. Mirrors pine-cpp / pine-java / pine-python.
type HttpStats struct {
	mu        sync.Mutex
	requests  map[string]int64
	durations map[string]*HttpDurationBucket
}

// NewHttpStats creates an empty HttpStats container.
func NewHttpStats() *HttpStats {
	return &HttpStats{
		requests:  make(map[string]int64),
		durations: make(map[string]*HttpDurationBucket),
	}
}

func (h *HttpStats) recordRequest(method, path, status string, durationNs int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reqKey := method + " " + path + " " + status
	h.requests[reqKey]++

	durKey := method + " " + path
	bucket, ok := h.durations[durKey]
	if !ok {
		bucket = &HttpDurationBucket{}
		h.durations[durKey] = bucket
	}
	bucket.Count++
	bucket.SumNs += durationNs
}

// Snapshot returns a deterministic snapshot of accumulated http stats.
// requests_total keys are "<METHOD> <path> <statusBucket>" sorted ascending.
// request_duration_seconds keys are "<METHOD> <path>" sorted ascending.
// Map iteration is key-sorted to keep JSON output byte-exact across runtimes.
func (h *HttpStats) Snapshot() map[string]any {
	h.mu.Lock()
	reqCopy := make(map[string]int64, len(h.requests))
	durCopy := make(map[string]HttpDurationBucket, len(h.durations))
	for k, v := range h.requests {
		reqCopy[k] = v
	}
	for k, v := range h.durations {
		durCopy[k] = *v
	}
	h.mu.Unlock()

	reqOut := make(map[string]int64, len(reqCopy))
	reqKeys := make([]string, 0, len(reqCopy))
	for k := range reqCopy {
		reqKeys = append(reqKeys, k)
	}
	sort.Strings(reqKeys)
	for _, k := range reqKeys {
		reqOut[k] = reqCopy[k]
	}

	durOut := make(map[string]HttpDurationBucket, len(durCopy))
	durKeys := make([]string, 0, len(durCopy))
	for k := range durCopy {
		durKeys = append(durKeys, k)
	}
	sort.Strings(durKeys)
	for _, k := range durKeys {
		durOut[k] = durCopy[k]
	}

	return map[string]any{
		"requests_total":           reqOut,
		"request_duration_seconds": durOut,
	}
}

func httpMetricsMiddleware(mp metrics.Provider, stats *HttpStats, next http.Handler) http.Handler {
	requestsTotal := mp.NewCounter(metrics.MetricOpts{
		Name:       "pine_http_requests_total",
		Help:       "Total HTTP requests.",
		LabelNames: []string{"method", "path", "status"},
	})
	requestDuration := mp.NewHistogram(metrics.HistogramOpts{
		MetricOpts: metrics.MetricOpts{
			Name:       "pine_http_request_duration_seconds",
			Help:       "HTTP request duration in seconds.",
			LabelNames: []string{"method", "path"},
		},
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0},
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		path := normalizePath(r.URL.Path)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		duration := time.Since(start)
		status := statusBucket(rec.status)

		requestsTotal.With(r.Method, path, status).Inc()
		requestDuration.With(r.Method, path).Observe(metrics.DurationSeconds(duration))

		if stats != nil {
			stats.recordRequest(r.Method, path, status, duration.Nanoseconds())
		}
	})
}
