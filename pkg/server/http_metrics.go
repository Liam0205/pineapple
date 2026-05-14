package server

import (
	"net/http"
	"time"

	"github.com/Liam0205/pineapple/pkg/metrics"
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
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	default:
		return "5xx"
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

func httpMetricsMiddleware(mp metrics.Provider, next http.Handler) http.Handler {
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
	})
}
