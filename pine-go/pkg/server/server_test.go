package server

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/internal/registry"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

// rmGetVal borrows a resource value via the handle API and releases it
// immediately, returning the value and ok.
func rmGetVal(rp resource.ResourceProvider, name string) (any, bool) {
	h, ok := rp.Get(name)
	if !ok {
		return nil, false
	}
	defer h.Release()
	return h.Value(), true
}

// --- test operator ---

type noopOp struct{}

func (o *noopOp) Init(params map[string]any) error { return nil }
func (o *noopOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	out.SetCommon("y", in.Common("x"))
	return nil
}

type benchCPUOp struct {
	work int
	salt uint64
	types.MetadataHolder
}

func (o *benchCPUOp) Init(params map[string]any) error {
	o.work = benchParamInt(params["work"], 1)
	o.salt = uint64(benchParamInt(params["salt"], 0))
	return nil
}

func (o *benchCPUOp) Execute(_ context.Context, in *types.OperatorInput, out *types.OperatorOutput) error {
	acc := uint64(0x9e3779b97f4a7c15) + o.salt
	for _, field := range o.CommonInput {
		acc ^= benchValueHash(in.Common(field)) + 0x9e3779b97f4a7c15 + (acc << 6) + (acc >> 2)
	}
	for i := 0; i < o.work; i++ {
		acc ^= acc << 13
		acc ^= acc >> 7
		acc ^= acc << 17
		acc += uint64(i+1) * 0xbf58476d1ce4e5b9
	}
	value := float64(acc & 0x1fffff)
	for i, field := range o.CommonOutput {
		out.SetCommon(field, value+float64(i))
	}
	return nil
}

func init() {
	registry.Reset()
	registry.Register(types.OperatorSchema{
		Name:        "noop",
		Type:        types.OpTypeTransform,
		Description: "No-op test operator.",
	}, func() types.Operator { return &noopOp{} })
	registry.Register(types.OperatorSchema{
		Name:        "bench_cpu",
		Type:        types.OpTypeTransform,
		Description: "CPU-bound benchmark operator.",
		Params: map[string]types.ParamSpec{
			"work": {Type: "int64", Required: false, Default: int64(1), Description: "CPU loop iterations."},
			"salt": {Type: "int64", Required: false, Default: int64(0), Description: "Per-operator hash salt."},
		},
	}, func() types.Operator { return &benchCPUOp{} })
}

var (
	benchDAGDepth = flag.Int("pineapple.bench.depth", 4, "Complex DAG benchmark depth.")
	benchDAGWidth = flag.Int("pineapple.bench.width", 16, "Complex DAG benchmark width per layer.")
	benchDAGFanIn = flag.Int("pineapple.bench.fanin", 2, "Complex DAG benchmark fan-in from previous layer.")
	benchWork     = flag.Int("pineapple.bench.work", 1000, "CPU loop iterations per benchmark operator.")
	benchItems    = flag.Int("pineapple.bench.items", 0, "Request item count for complex DAG benchmark.")
	benchWorkers  = flag.Int("pineapple.bench.workers", 0, "HTTP benchmark workers; 0 means 2*GOMAXPROCS.")
	benchReload   = flag.Bool("pineapple.bench.reload", false, "Reload config every 10ms during complex DAG benchmark.")
)

// --- helpers ---

func minimalConfig(t testing.TB, resConfig map[string]any) []byte {
	t.Helper()
	cfg := map[string]any{
		"_PINEAPPLE_VERSION": pine.Version,
		"pipeline_config": map[string]any{
			"operators": map[string]any{
				"op_a_A1B2C3": map[string]any{
					"type_name": "noop",
					"$metadata": map[string]any{
						"common_input":  []string{"x"},
						"common_output": []string{"y"},
						"item_input":    []string{},
						"item_output":   []string{},
					},
				},
			},
			"pipeline_map": map[string]any{
				"stage1": map[string]any{
					"pipeline": []string{"op_a_A1B2C3"},
				},
			},
		},
		"pipeline_group": map[string]any{
			"main": map[string]any{
				"pipeline": []string{"stage1"},
			},
		},
		"flow_contract": map[string]any{
			"common_input":  []string{"x"},
			"item_input":    []string{},
			"common_output": []string{"y"},
			"item_output":   []string{},
		},
	}
	if resConfig != nil {
		cfg["resource_config"] = resConfig
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTempConfig(t testing.TB, data []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "config-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return f.Name()
}

// --- tests ---

func TestReloadConfig_EngineAndResources(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	var callCount int
	resource.Register(types.ResourceSchema{
		Name:            "test_res",
		Description:     "test resource",
		DefaultInterval: 3600,
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			callCount++
			return fmt.Sprintf("value_%d", callCount), nil
		}, nil
	})

	cfg1 := minimalConfig(t, map[string]any{
		"my_res": map[string]any{
			"type":     "test_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path := writeTempConfig(t, cfg1)

	s := &Server{}

	// Initial load via reloadConfig
	if err := s.reloadConfig(path); err != nil {
		t.Fatalf("initial reloadConfig failed: %v", err)
	}
	defer func() {
		if snap := s.snapshot.Load(); snap != nil {
			snap.resources.Stop()
		}
	}()

	// Verify engine and resources are loaded
	snap := s.snapshot.Load()
	if snap == nil || snap.engine == nil {
		t.Fatal("engine should be loaded")
	}
	rm := snap.resources
	if rm == nil {
		t.Fatal("resources should be loaded")
	}
	val, ok := rmGetVal(rm, "my_res")
	if !ok {
		t.Fatal("expected my_res to exist")
	}
	if val != "value_1" {
		t.Errorf("val = %v, want value_1", val)
	}

	// Reload — should create new Manager
	oldRM := rm
	if err := s.reloadConfig(path); err != nil {
		t.Fatalf("second reloadConfig failed: %v", err)
	}

	newRM := s.snapshot.Load().resources
	if newRM == oldRM {
		t.Error("expected new Manager after reload")
	}
	val, ok = rmGetVal(newRM, "my_res")
	if !ok {
		t.Fatal("expected my_res in new Manager")
	}
	if val != "value_2" {
		t.Errorf("val = %v, want value_2", val)
	}
}

func TestReloadConfig_ResourceStartFailure(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	resource.Register(types.ResourceSchema{
		Name:            "good_res",
		Description:     "always works",
		DefaultInterval: 3600,
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "ok", nil
		}, nil
	})

	resource.Register(types.ResourceSchema{
		Name:            "bad_res",
		Description:     "always fails on fetch",
		DefaultInterval: 3600,
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return nil, fmt.Errorf("connection refused")
		}, nil
	})

	s := &Server{}

	// Initial config with good resource only
	cfg1 := minimalConfig(t, map[string]any{
		"r1": map[string]any{
			"type":     "good_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path1 := writeTempConfig(t, cfg1)
	if err := s.reloadConfig(path1); err != nil {
		t.Fatalf("initial reloadConfig failed: %v", err)
	}
	defer func() {
		if snap := s.snapshot.Load(); snap != nil {
			snap.resources.Stop()
		}
	}()

	origSnap := s.snapshot.Load()

	// Config with bad resource — Start should fail
	cfg2 := minimalConfig(t, map[string]any{
		"r1": map[string]any{
			"type":     "bad_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path2 := writeTempConfig(t, cfg2)
	err := s.reloadConfig(path2)
	if err == nil {
		t.Fatal("expected error from bad resource")
	}

	// Engine and resources should be unchanged
	curSnap := s.snapshot.Load()
	if curSnap.engine != origSnap.engine {
		t.Error("engine should not change on failed reload")
	}
	if curSnap.resources != origSnap.resources {
		t.Error("resources should not change on failed reload")
	}

	// Old resources still work
	val, ok := rmGetVal(origSnap.resources, "r1")
	if !ok || val != "ok" {
		t.Errorf("old resource should still work, got %v, %v", val, ok)
	}
}

func TestReloadConfig_NoResources(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	cfg := minimalConfig(t, nil)
	path := writeTempConfig(t, cfg)

	s := &Server{}

	if err := s.reloadConfig(path); err != nil {
		t.Fatalf("reloadConfig with no resources failed: %v", err)
	}
	defer func() {
		if snap := s.snapshot.Load(); snap != nil {
			snap.resources.Stop()
		}
	}()

	snap := s.snapshot.Load()
	if snap == nil || snap.engine == nil {
		t.Fatal("engine should be loaded")
	}
	rm := snap.resources
	if rm == nil {
		t.Fatal("resources manager should exist even without resources")
	}
	if len(rm.Names()) != 0 {
		t.Errorf("expected no resources, got %v", rm.Names())
	}
}

func TestReloadConfig_OldManagerStopped(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	var activeCount int32
	resource.Register(types.ResourceSchema{
		Name:            "track_res",
		Description:     "tracks active fetches",
		DefaultInterval: 3600,
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "data", nil
		}, nil
	})

	cfg := minimalConfig(t, map[string]any{
		"r": map[string]any{
			"type":     "track_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path := writeTempConfig(t, cfg)

	s := &Server{}

	if err := s.reloadConfig(path); err != nil {
		t.Fatal(err)
	}
	// Borrow a handle from the old Manager BEFORE it is retired, simulating an
	// in-flight request that captured the old snapshot. The borrow must keep
	// the value alive across retirement.
	origSnap := s.snapshot.Load()
	inflight, ok := origSnap.resources.Get("r")
	if !ok {
		t.Fatal("expected r to be loaded in old Manager")
	}

	// Reload to replace; this retires the old snapshot (drops its baseline
	// reference). Because an in-flight borrow is still held, teardown is
	// deferred and the borrowed value stays readable.
	if err := s.reloadConfig(path); err != nil {
		t.Fatal(err)
	}
	defer s.snapshot.Load().resources.Stop()

	if inflight.Value() != "data" {
		t.Errorf("in-flight borrow value = %v, want data", inflight.Value())
	}
	inflight.Release()

	// After the old snapshot is retired AND every borrow released, a fresh Get
	// on the old Manager returns ok=false — its values have been released.
	if _, ok := origSnap.resources.Get("r"); ok {
		t.Error("retired Manager should not hand out new borrows after teardown")
	}

	_ = activeCount
}

func TestWatchConfigIntegration(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	resource.Register(types.ResourceSchema{
		Name:            "watch_res",
		Description:     "for watch test",
		DefaultInterval: 3600,
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			return "initial", nil
		}, nil
	})

	cfg := minimalConfig(t, map[string]any{
		"wr": map[string]any{
			"type":     "watch_res",
			"interval": 3600,
			"params":   map[string]any{},
		},
	})
	path := writeTempConfig(t, cfg)

	s := &Server{}

	// Do initial load
	if err := s.reloadConfig(path); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if snap := s.snapshot.Load(); snap != nil {
			snap.resources.Stop()
		}
	}()

	rm := s.snapshot.Load().resources
	val, _ := rmGetVal(rm, "wr")
	if val != "initial" {
		t.Errorf("val = %v, want initial", val)
	}

	// Touch the file to trigger a reload
	time.Sleep(10 * time.Millisecond)
	if err := os.Chtimes(path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}

	// reloadConfig should succeed when called directly (simulating watchConfig behavior)
	if err := s.reloadConfig(path); err != nil {
		t.Fatalf("manual reload failed: %v", err)
	}
}

// --- HTTP handler tests ---

func setupEngine(t *testing.T) *Server {
	t.Helper()
	cfg := minimalConfig(t, nil)
	engine, err := pine.NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	rm := resource.NewManager(nil)
	s := &Server{}
	s.snapshot.Store(newSnapshot(engine, rm, nil))
	t.Cleanup(func() {
		s.snapshot.Store(nil)
	})
	return s
}

func setupTestHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := setupEngine(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", s.handleExecute)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/dag", s.handleDAG)
	mux.HandleFunc("/", handleNotFound)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleHealth(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %q, want status ok", w.Body.String())
	}
}

func TestHandleExecute_Success(t *testing.T) {
	s := setupEngine(t)

	body := `{"common":{"x":42},"items":[]}`
	req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleExecute(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp executeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error != "" {
		t.Errorf("unexpected error: %s", resp.Error)
	}
}

func TestHandleExecute_WithTrace(t *testing.T) {
	s := setupEngine(t)

	body := `{"common":{"x":42,"_return_trace":true},"items":[]}`
	req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleExecute(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp executeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Trace) == 0 {
		t.Error("expected trace entries when _return_trace=true")
	}
}

func TestHandleExecute_MethodNotAllowed(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/execute", nil)
	w := httptest.NewRecorder()
	s.handleExecute(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleExecute_EngineNotLoaded(t *testing.T) {
	s := &Server{}
	s.snapshot.Store(nil)

	body := `{"common":{"x":1}}`
	req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleExecute(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleExecute_BadJSON(t *testing.T) {
	s := setupEngine(t)

	req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader("{invalid"))
	w := httptest.NewRecorder()
	s.handleExecute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestExecuteRequestBodyTooLarge(t *testing.T) {
	s := setupEngine(t)

	// Create a JSON body larger than 10 MB.
	// Start with valid JSON structure containing a huge string value.
	prefix := []byte(`{"common":{"x":"`)
	suffix := []byte(`"}}`)
	padding := make([]byte, 11<<20) // 11 MB of 'a'
	for i := range padding {
		padding[i] = 'a'
	}
	bigBody := make([]byte, 0, len(prefix)+len(padding)+len(suffix))
	bigBody = append(bigBody, prefix...)
	bigBody = append(bigBody, padding...)
	bigBody = append(bigBody, suffix...)

	req := httptest.NewRequest(http.MethodPost, "/execute", bytes.NewReader(bigBody))
	w := httptest.NewRecorder()
	s.handleExecute(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "too large") && !strings.Contains(body, "request body") {
		t.Errorf("expected error about body size, got: %s", body)
	}
}

func TestHandleExecute_ValidationError_Returns400(t *testing.T) {
	s := setupEngine(t)

	// The test engine's flow_contract requires common field "x".
	// Sending a request without "x" should trigger ValidationError → 400.
	body := `{"common":{"missing_field":1},"items":[]}`
	req := httptest.NewRequest(http.MethodPost, "/execute", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleExecute(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for ValidationError", w.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	errMsg, _ := resp["error"].(string)
	if !strings.Contains(errMsg, "missing required common input field") {
		t.Errorf("error = %q, want mention of missing field", errMsg)
	}
}

func TestHandleStats_Success(t *testing.T) {
	s := setupEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	w := httptest.NewRecorder()
	s.handleStats(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var stats map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
}

func TestHandleStats_MethodNotAllowed(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/stats", nil)
	w := httptest.NewRecorder()
	s.handleStats(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleStats_EngineNotLoaded(t *testing.T) {
	s := &Server{}
	s.snapshot.Store(nil)

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	w := httptest.NewRecorder()
	s.handleStats(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleDAG_Dot(t *testing.T) {
	s := setupEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/dag", nil)
	w := httptest.NewRecorder()
	s.handleDAG(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "graphviz") {
		t.Errorf("Content-Type = %q, want graphviz", ct)
	}
	if !strings.Contains(w.Body.String(), "digraph") {
		t.Error("expected DOT output containing 'digraph'")
	}
}

func TestHandleDAG_Mermaid(t *testing.T) {
	s := setupEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/dag?format=mermaid", nil)
	w := httptest.NewRecorder()
	s.handleDAG(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "graph TB") {
		t.Error("expected Mermaid output containing 'graph TB'")
	}
}

func TestHandleDAG_MethodNotAllowed(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/dag", nil)
	w := httptest.NewRecorder()
	s.handleDAG(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}

func TestHandleDAG_EngineNotLoaded(t *testing.T) {
	s := &Server{}
	s.snapshot.Store(nil)

	req := httptest.NewRequest(http.MethodGet, "/dag", nil)
	w := httptest.NewRecorder()
	s.handleDAG(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestHandleDAG_BadFormat(t *testing.T) {
	s := setupEngine(t)

	req := httptest.NewRequest(http.MethodGet, "/dag?format=xyz", nil)
	w := httptest.NewRecorder()
	s.handleDAG(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHTTPServerConcurrentExecuteStatsAndDAG(t *testing.T) {
	srv := setupTestHTTPServer(t)
	client := srv.Client()
	client.Timeout = 5 * time.Second

	workers := runtime.GOMAXPROCS(0) * 2
	if workers < 8 {
		workers = 8
	}
	if workers > 64 {
		workers = 64
	}
	requests := workers * 8

	var wg sync.WaitGroup
	errs := make(chan error, requests+2*workers)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := postExecute(client, srv.URL, i); err != nil {
				errs <- err
			}
		}(i)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := getOK(client, srv.URL+"/stats"); err != nil {
				errs <- err
			}
			if err := getOK(client, srv.URL+"/dag"); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	statsResp, err := client.Get(srv.URL + "/stats")
	if err != nil {
		t.Fatal(err)
	}
	defer statsResp.Body.Close()
	if statsResp.StatusCode != http.StatusOK {
		t.Fatalf("/stats status = %d", statsResp.StatusCode)
	}
	var stats map[string]any
	if err := json.NewDecoder(statsResp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode /stats: %v", err)
	}
	scheduler, ok := stats["scheduler"].(map[string]any)
	if !ok {
		t.Fatalf("missing scheduler stats: %v", stats)
	}
	if got := int(scheduler["run_count"].(float64)); got < requests {
		t.Fatalf("scheduler run_count = %d, want at least %d", got, requests)
	}
}

func TestServerHighConcurrencyStress(t *testing.T) {
	if os.Getenv("PINEAPPLE_STRESS") != "1" {
		t.Skip("set PINEAPPLE_STRESS=1 to run high-concurrency server stress test")
	}

	cfg := minimalConfig(t, nil)
	path := writeTempConfig(t, cfg)

	s := &Server{}
	if err := s.reloadConfig(path); err != nil {
		t.Fatalf("initial reloadConfig: %v", err)
	}
	t.Cleanup(func() {
		if snap := s.snapshot.Load(); snap != nil {
			snap.resources.Stop()
		}
		s.snapshot.Store(nil)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/execute", s.handleExecute)
	mux.HandleFunc("/stats", s.handleStats)
	mux.HandleFunc("/dag", s.handleDAG)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := srv.Client()
	client.Timeout = 10 * time.Second
	workers := runtime.GOMAXPROCS(0) * 2
	if workers < 32 {
		workers = 32
	}
	iterations := 64

	stopReload := make(chan struct{})
	var reloadErrors atomic.Int64
	var reloadWG sync.WaitGroup
	reloadWG.Add(1)
	go func() {
		defer reloadWG.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopReload:
				return
			case <-ticker.C:
				if err := s.reloadConfig(path); err != nil {
					reloadErrors.Add(1)
				}
			}
		}
	}()

	var wg sync.WaitGroup
	statsChecks := (iterations + 15) / 16
	errs := make(chan error, workers*(iterations+statsChecks))
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				value := worker*iterations + i
				if err := postExecute(client, srv.URL, value); err != nil {
					errs <- err
				}
				if i%16 == 0 {
					if err := getOK(client, srv.URL+"/stats"); err != nil {
						errs <- err
					}
				}
			}
		}(worker)
	}
	wg.Wait()
	close(stopReload)
	reloadWG.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
	if got := reloadErrors.Load(); got != 0 {
		t.Fatalf("reload errors = %d", got)
	}
}

func BenchmarkHTTPServerExecuteThroughput(b *testing.B) {
	procs := runtime.GOMAXPROCS(0)
	workerCases := []int{procs, procs * 2}
	for _, reload := range []bool{false, true} {
		for _, workers := range workerCases {
			name := fmt.Sprintf("workers=%d/reload=%t", workers, reload)
			b.Run(name, func(b *testing.B) {
				benchmarkHTTPServerExecuteThroughput(
					b,
					minimalConfig(b, nil),
					workers,
					reload,
					func(client *http.Client, baseURL string, value int) error {
						return postExecute(client, baseURL, value)
					},
				)
			})
		}
	}
}

func BenchmarkHTTPServerComplexDAGThroughput(b *testing.B) {
	workers := *benchWorkers
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0) * 2
	}
	cfg := complexBenchmarkConfig(
		b,
		clampBenchInt(*benchDAGDepth, 1, 64),
		clampBenchInt(*benchDAGWidth, 1, 256),
		clampBenchInt(*benchDAGFanIn, 0, 64),
		clampBenchInt(*benchWork, 1, 1_000_000),
	)
	body := complexBenchmarkRequestBody(b, clampBenchInt(*benchItems, 0, 100_000))
	b.ReportMetric(float64(clampBenchInt(*benchDAGDepth, 1, 64)), "dag_depth")
	b.ReportMetric(float64(clampBenchInt(*benchDAGWidth, 1, 256)), "dag_width")
	b.ReportMetric(float64(clampBenchInt(*benchDAGFanIn, 0, 64)), "dag_fanin")
	b.ReportMetric(float64(clampBenchInt(*benchWork, 1, 1_000_000)), "op_work")
	b.ReportMetric(float64(clampBenchInt(*benchItems, 0, 100_000)), "items")
	b.ReportMetric(float64(workers), "workers")
	benchmarkHTTPServerExecuteThroughput(
		b,
		cfg,
		workers,
		*benchReload,
		func(client *http.Client, baseURL string, _ int) error {
			return postExecuteBody(client, baseURL, body)
		},
	)
}

func benchmarkHTTPServerExecuteThroughput(
	b *testing.B,
	cfg []byte,
	workers int,
	reload bool,
	post func(client *http.Client, baseURL string, value int) error,
) {
	path := writeTempConfig(b, cfg)

	s := &Server{}
	if err := s.reloadConfig(path); err != nil {
		b.Fatalf("initial reloadConfig: %v", err)
	}
	b.Cleanup(func() {
		if snap := s.snapshot.Load(); snap != nil {
			snap.resources.Stop()
		}
		s.snapshot.Store(nil)
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/execute", s.handleExecute)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := benchmarkHTTPClient(workers)
	defer client.CloseIdleConnections()

	stopReload := make(chan struct{})
	var reloadWG sync.WaitGroup
	if reload {
		reloadWG.Add(1)
		go func() {
			defer reloadWG.Done()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopReload:
					return
				case <-ticker.C:
					_ = s.reloadConfig(path)
				}
			}
		}()
	}

	var next atomic.Int64
	var errOnce sync.Once
	var firstErr error

	b.ReportAllocs()
	b.ResetTimer()
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= b.N {
					return
				}
				if err := post(client, srv.URL, i); err != nil {
					errOnce.Do(func() { firstErr = err })
					return
				}
			}
		}()
	}
	wg.Wait()
	b.StopTimer()

	if reload {
		close(stopReload)
		reloadWG.Wait()
	}
	if firstErr != nil {
		b.Fatal(firstErr)
	}
}

func complexBenchmarkConfig(b testing.TB, depth, width, fanIn, work int) []byte {
	b.Helper()
	operators := make(map[string]any, depth*width)
	pipeline := make([]string, 0, depth*width)
	prevFields := []string{"x"}

	for layer := 0; layer < depth; layer++ {
		nextFields := make([]string, width)
		for slot := 0; slot < width; slot++ {
			name := fmt.Sprintf("bench_l%d_n%d", layer, slot)
			outField := fmt.Sprintf("f_%d_%d", layer, slot)
			if layer == depth-1 && slot == 0 {
				outField = "y"
			}
			inputs := selectBenchInputs(prevFields, slot, fanIn)
			operators[name] = map[string]any{
				"type_name": "bench_cpu",
				"work":      float64(work),
				"salt":      float64(layer*width + slot + 1),
				"$metadata": map[string]any{
					"common_input":  inputs,
					"common_output": []string{outField},
					"item_input":    []string{},
					"item_output":   []string{},
				},
			}
			pipeline = append(pipeline, name)
			nextFields[slot] = outField
		}
		prevFields = nextFields
	}

	cfg := map[string]any{
		"_PINEAPPLE_VERSION": pine.Version,
		"pipeline_config": map[string]any{
			"operators": operators,
			"pipeline_map": map[string]any{
				"stage1": map[string]any{"pipeline": pipeline},
			},
		},
		"pipeline_group": map[string]any{
			"main": map[string]any{"pipeline": []string{"stage1"}},
		},
		"flow_contract": map[string]any{
			"common_input":  []string{"x"},
			"item_input":    []string{},
			"common_output": []string{"y"},
			"item_output":   []string{},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		b.Fatal(err)
	}
	return data
}

func selectBenchInputs(fields []string, slot, fanIn int) []string {
	if fanIn <= 0 || len(fields) == 0 {
		return nil
	}
	if fanIn > len(fields) {
		fanIn = len(fields)
	}
	inputs := make([]string, fanIn)
	for i := 0; i < fanIn; i++ {
		inputs[i] = fields[(slot+i)%len(fields)]
	}
	return inputs
}

func complexBenchmarkRequestBody(b testing.TB, items int) []byte {
	b.Helper()
	req := executeRequest{
		Common: map[string]any{"x": 1.0},
	}
	if items > 0 {
		req.Items = make([]map[string]any, items)
		for i := range req.Items {
			req.Items[i] = map[string]any{
				"id":    float64(i),
				"score": float64(i % 100),
			}
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		b.Fatal(err)
	}
	return body
}

func benchmarkHTTPClient(workers int) *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        workers * 2,
			MaxIdleConnsPerHost: workers * 2,
			MaxConnsPerHost:     workers * 2,
		},
	}
}

func postExecuteBody(client *http.Client, baseURL string, body []byte) error {
	resp, err := client.Post(baseURL+"/execute", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/execute status = %d", resp.StatusCode)
	}
	var out executeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode /execute: %w", err)
	}
	if out.Error != "" {
		return fmt.Errorf("/execute error: %s", out.Error)
	}
	if _, ok := out.Common["y"].(float64); !ok {
		return fmt.Errorf("/execute missing numeric y: %v", out.Common["y"])
	}
	return nil
}

func postExecute(client *http.Client, baseURL string, value int) error {
	body := []byte(fmt.Sprintf(`{"common":{"x":%d},"items":[]}`, value))
	resp, err := client.Post(baseURL+"/execute", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("/execute status = %d", resp.StatusCode)
	}
	var out executeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("decode /execute: %w", err)
	}
	if out.Error != "" {
		return fmt.Errorf("/execute error: %s", out.Error)
	}
	got, ok := out.Common["y"].(float64)
	if !ok || got != float64(value) {
		return fmt.Errorf("/execute y = %v (%T), want %d", out.Common["y"], out.Common["y"], value)
	}
	return nil
}

func benchParamInt(v any, fallback int) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	default:
		return fallback
	}
}

func benchValueHash(v any) uint64 {
	switch x := v.(type) {
	case int:
		return uint64(x)
	case int64:
		return uint64(x)
	case float64:
		return uint64(x * 1000)
	case string:
		var h uint64 = 1469598103934665603
		for i := 0; i < len(x); i++ {
			h ^= uint64(x[i])
			h *= 1099511628211
		}
		return h
	case bool:
		if x {
			return 1
		}
		return 0
	default:
		return 0
	}
}

func clampBenchInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func getOK(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s status = %d", url, resp.StatusCode)
	}
	return nil
}

func TestMiddlewareApplied(t *testing.T) {
	s := setupEngine(t)
	_ = s // Server instance available if needed

	var order []string
	makeMiddleware := func(tag string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, tag+"-before")
				next.ServeHTTP(w, r)
				order = append(order, tag+"-after")
			})
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)

	middlewares := []func(http.Handler) http.Handler{
		makeMiddleware("outer"),
		makeMiddleware("inner"),
	}
	var handler http.Handler = mux
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	expected := []string{"outer-before", "inner-before", "inner-after", "outer-after"}
	if len(order) != len(expected) {
		t.Fatalf("middleware call order = %v, want %v", order, expected)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %q, want %q", i, order[i], v)
		}
	}
}

func TestHandleNotFound_ReturnsJSON404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/unknown-path", nil)
	w := httptest.NewRecorder()
	handleNotFound(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["error"] != "not found" {
		t.Errorf("error = %q, want \"not found\"", resp["error"])
	}
}

func TestMiddlewareCanInterceptCustomPath(t *testing.T) {
	s := setupEngine(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/execute", s.handleExecute)
	mux.HandleFunc("/", handleNotFound)

	customHandler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/metrics" {
				writeJSON(w, http.StatusOK, map[string]any{"custom": true})
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	handler := customHandler(mux)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	client := srv.Client()

	// Custom path intercepted by middleware
	resp, err := client.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/metrics status = %d, want 200", resp.StatusCode)
	}
	var metricsBody map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&metricsBody); err != nil {
		t.Fatal(err)
	}
	if metricsBody["custom"] != true {
		t.Errorf("expected custom=true from middleware, got %v", metricsBody)
	}

	// Known path still works
	resp2, err := client.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("/health status = %d, want 200", resp2.StatusCode)
	}

	// Unknown path returns 404 JSON (not intercepted)
	resp3, err := client.Get(srv.URL + "/other")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("/other status = %d, want 404", resp3.StatusCode)
	}
}

func TestNoMiddlewareNoop(t *testing.T) {
	s := setupEngine(t)
	_ = s // Server instance available if needed

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)

	var middlewares []func(http.Handler) http.Handler
	var handler http.Handler = mux
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// TestSnapshotDrainDefersTeardown verifies that retiring a snapshot
// (dropping its baseline reference) defers engine/resource teardown until the
// last in-flight reference is released.
func TestSnapshotDrainDefersTeardown(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	cfg := minimalConfig(t, nil)
	path := writeTempConfig(t, cfg)

	s := &Server{}
	if err := s.reloadConfig(path); err != nil {
		t.Fatal(err)
	}

	snap := s.acquireSnapshot() // simulate an in-flight request holding a ref
	if snap == nil {
		t.Fatal("expected a live snapshot")
	}

	// Retire by reloading: drops the baseline reference on the old snapshot.
	if err := s.reloadConfig(path); err != nil {
		t.Fatal(err)
	}
	defer s.snapshot.Load().release()

	// The engine of the retired snapshot must still be usable: teardown is
	// deferred because our in-flight reference is still held. A nil/closed
	// engine would fail here. Engine.Close on a Lua-less pipeline is a no-op,
	// so we assert indirectly: the retired snapshot must still be acquirable
	// by us (refs > 0) and Execute must succeed.
	out, err := snap.engine.Execute(
		resource.WithResources(context.Background(), snap.resources),
		&pine.Request{Common: map[string]any{"x": 1.0}},
	)
	if err != nil {
		t.Fatalf("retired engine should still serve in-flight request: %v", err)
	}
	if out == nil {
		t.Fatal("expected a result from retired engine")
	}

	// Releasing the final in-flight reference runs teardown exactly once. The
	// deferred release above targets the current live snapshot — a different
	// object — so there is no double-free.
	snap.release()
}
