package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
)

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/execute", "/execute"},
		{"/health", "/health"},
		{"/stats", "/stats"},
		{"/dag", "/dag"},
		{"/unknown", "_other"},
		{"/execute/extra", "_other"},
		{"/", "_other"},
	}
	for _, tt := range tests {
		if got := normalizePath(tt.input); got != tt.want {
			t.Errorf("normalizePath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStatusBucket(t *testing.T) {
	tests := []struct {
		code int
		want string
	}{
		{200, "2xx"},
		{201, "2xx"},
		{204, "2xx"},
		{301, "3xx"},
		{400, "4xx"},
		{404, "4xx"},
		{500, "5xx"},
		{503, "5xx"},
	}
	for _, tt := range tests {
		if got := statusBucket(tt.code); got != tt.want {
			t.Errorf("statusBucket(%d) = %q, want %q", tt.code, got, tt.want)
		}
	}
}

func TestStatusRecorder(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	rec.WriteHeader(http.StatusNotFound)
	if rec.status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.status, http.StatusNotFound)
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("underlying recorder code = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestStatusRecorderDefaultStatus(t *testing.T) {
	w := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

	_, _ = rec.Write([]byte("hello"))
	if rec.status != http.StatusOK {
		t.Errorf("status = %d, want %d (default)", rec.status, http.StatusOK)
	}
}

func TestHTTPMetricsMiddleware_Integration(t *testing.T) {
	mp := &recordingProvider{}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	})

	handler := httpMetricsMiddleware(mp, NewHttpStats(), mux)

	// GET /health → 200
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("/health status = %d", w.Code)
	}

	// POST /execute → 400
	req = httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader("{}"))
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("/execute status = %d", w.Code)
	}

	// GET /unknown → 404 (mux default)
	req = httptest.NewRequest(http.MethodGet, "/unknown", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	// Verify counter labels
	counter := mp.counter
	counter.mu.Lock()
	defer counter.mu.Unlock()

	found200 := false
	found400 := false
	foundOther := false
	for _, call := range counter.incs {
		if call == "GET|/health|2xx" {
			found200 = true
		}
		if call == "POST|/execute|4xx" {
			found400 = true
		}
		if strings.Contains(call, "_other") {
			foundOther = true
		}
	}
	if !found200 {
		t.Errorf("missing counter for GET /health 2xx; got %v", counter.incs)
	}
	if !found400 {
		t.Errorf("missing counter for POST /execute 4xx; got %v", counter.incs)
	}
	if !foundOther {
		t.Errorf("missing counter for unknown path → _other; got %v", counter.incs)
	}

	// Verify histogram observations
	histogram := mp.histogram
	histogram.mu.Lock()
	defer histogram.mu.Unlock()

	if len(histogram.observations) != 3 {
		t.Errorf("histogram observations = %d, want 3", len(histogram.observations))
	}
	for _, obs := range histogram.observations {
		if obs.value <= 0 {
			t.Errorf("histogram observation = %v, want > 0", obs.value)
		}
	}
}

func TestHTTPMetricsMiddleware_NopProvider(t *testing.T) {
	mp := metrics.Nop()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)

	handler := httpMetricsMiddleware(mp, NewHttpStats(), mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// --- recording metrics provider for testing ---

type recordingProvider struct {
	counter   *recordingCounter
	histogram *recordingHistogram
}

func (p *recordingProvider) NewCounter(opts metrics.MetricOpts) metrics.Counter {
	p.counter = &recordingCounter{}
	return p.counter
}

func (p *recordingProvider) NewGauge(opts metrics.MetricOpts) metrics.Gauge {
	return metrics.Nop().NewGauge(opts)
}

func (p *recordingProvider) NewHistogram(opts metrics.HistogramOpts) metrics.Histogram {
	p.histogram = &recordingHistogram{}
	return p.histogram
}

type recordingCounter struct {
	mu   sync.Mutex
	incs []string
}

func (c *recordingCounter) With(labelValues ...string) metrics.Counter {
	return &recordingCounterWith{parent: c, key: strings.Join(labelValues, "|")}
}

func (c *recordingCounter) Inc() {
	c.mu.Lock()
	c.incs = append(c.incs, "")
	c.mu.Unlock()
}

type recordingCounterWith struct {
	parent *recordingCounter
	key    string
}

func (c *recordingCounterWith) With(labelValues ...string) metrics.Counter {
	return &recordingCounterWith{parent: c.parent, key: c.key + "|" + strings.Join(labelValues, "|")}
}

func (c *recordingCounterWith) Inc() {
	c.parent.mu.Lock()
	c.parent.incs = append(c.parent.incs, c.key)
	c.parent.mu.Unlock()
}

type observation struct {
	key   string
	value float64
}

type recordingHistogram struct {
	mu           sync.Mutex
	observations []observation
}

func (h *recordingHistogram) With(labelValues ...string) metrics.Histogram {
	return &recordingHistogramWith{parent: h, key: strings.Join(labelValues, "|")}
}

func (h *recordingHistogram) Observe(v float64) {
	h.mu.Lock()
	h.observations = append(h.observations, observation{key: "", value: v})
	h.mu.Unlock()
}

type recordingHistogramWith struct {
	parent *recordingHistogram
	key    string
}

func (h *recordingHistogramWith) With(labelValues ...string) metrics.Histogram {
	return &recordingHistogramWith{parent: h.parent, key: h.key + "|" + strings.Join(labelValues, "|")}
}

func (h *recordingHistogramWith) Observe(v float64) {
	h.parent.mu.Lock()
	h.parent.observations = append(h.parent.observations, observation{key: h.key, value: v})
	h.parent.mu.Unlock()
}

func TestHttpStats_RecordAndSnapshot(t *testing.T) {
	mp := metrics.Nop()
	stats := NewHttpStats()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadRequest)
	})
	handler := httpMetricsMiddleware(mp, stats, mux)

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest(http.MethodPost, "/execute", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	req = httptest.NewRequest(http.MethodGet, "/totally-unknown", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	snap := stats.Snapshot()

	reqs := snap["requests_total"].(map[string]int64)
	if reqs["GET /health 2xx"] != 3 {
		t.Errorf("GET /health 2xx = %d, want 3", reqs["GET /health 2xx"])
	}
	if reqs["POST /execute 4xx"] != 1 {
		t.Errorf("POST /execute 4xx = %d, want 1", reqs["POST /execute 4xx"])
	}
	if reqs["GET _other 4xx"] != 1 {
		t.Errorf("GET _other 4xx = %d, want 1; got reqs=%v", reqs["GET _other 4xx"], reqs)
	}

	durs := snap["request_duration_seconds"].(map[string]HttpDurationBucket)
	if durs["GET /health"].Count != 3 {
		t.Errorf("GET /health duration count = %d, want 3", durs["GET /health"].Count)
	}
	if durs["GET /health"].SumNs <= 0 {
		t.Errorf("GET /health sum_ns = %d, want > 0", durs["GET /health"].SumNs)
	}
	if durs["POST /execute"].Count != 1 {
		t.Errorf("POST /execute duration count = %d, want 1", durs["POST /execute"].Count)
	}
}

func TestHttpStats_NilSafeInMiddleware(t *testing.T) {
	mp := metrics.Nop()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	handler := httpMetricsMiddleware(mp, nil, mux)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestHttpStats_SnapshotKeyOrdering(t *testing.T) {
	stats := NewHttpStats()
	stats.recordRequest("POST", "/execute", "2xx", 1000)
	stats.recordRequest("GET", "/health", "2xx", 500)
	stats.recordRequest("GET", "/stats", "2xx", 200)
	stats.recordRequest("GET", "/health", "2xx", 300)

	snap := stats.Snapshot()
	reqs := snap["requests_total"].(map[string]int64)
	keys := make([]string, 0, len(reqs))
	for k := range reqs {
		keys = append(keys, k)
	}
	// Map iteration order is not stable in Go, but JSON encoding of the map
	// will sort keys (encoding/json sorts string-keyed map keys).
	// We assert the keys produced by recordRequest are the expected set.
	want := map[string]int64{
		"POST /execute 2xx": 1,
		"GET /health 2xx":   2,
		"GET /stats 2xx":    1,
	}
	for k, v := range want {
		if reqs[k] != v {
			t.Errorf("requests_total[%q] = %d, want %d", k, reqs[k], v)
		}
	}
	if len(reqs) != len(want) {
		t.Errorf("requests_total keys = %v, want %v", keys, want)
	}
}
