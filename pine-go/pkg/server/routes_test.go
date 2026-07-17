package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

// Tests for the issue #169 upstreaming: custom Routes (Ingress/Egress),
// the Watch toggle, and the embedding API (NewServer / Execute / Acquire /
// Close). Mirrored by pine-java and pine-cpp server tests.

func passthroughIngress(r *http.Request) (*pine.Request, error) {
	return &pine.Request{Common: map[string]any{"x": 1.0}}, nil
}

func discardEgress(w http.ResponseWriter, r *http.Request, result *pine.Result, err error) {}

func TestValidateRoutes(t *testing.T) {
	valid := Route{Method: http.MethodPost, Path: "/api/v1/report", Ingress: passthroughIngress, Egress: discardEgress}
	cases := []struct {
		name    string
		routes  []Route
		wantErr string // empty means no error
	}{
		{"no routes", nil, ""},
		{"valid route", []Route{valid}, ""},
		{"empty path", []Route{{Path: "", Ingress: passthroughIngress, Egress: discardEgress}}, "must start with '/'"},
		{"relative path", []Route{{Path: "api", Ingress: passthroughIngress, Egress: discardEgress}}, "must start with '/'"},
		{"root path", []Route{{Path: "/", Ingress: passthroughIngress, Egress: discardEgress}}, "not-found handler"},
		{"conflicts with /execute", []Route{{Path: "/execute", Ingress: passthroughIngress, Egress: discardEgress}}, "conflicts with built-in"},
		{"conflicts with /health", []Route{{Path: "/health", Ingress: passthroughIngress, Egress: discardEgress}}, "conflicts with built-in"},
		{"conflicts with /stats", []Route{{Path: "/stats", Ingress: passthroughIngress, Egress: discardEgress}}, "conflicts with built-in"},
		{"conflicts with /dag", []Route{{Path: "/dag", Ingress: passthroughIngress, Egress: discardEgress}}, "conflicts with built-in"},
		{"duplicate", []Route{valid, valid}, "duplicate custom route"},
		{"nil ingress", []Route{{Path: "/a", Egress: discardEgress}}, "nil Ingress"},
		{"nil egress", []Route{{Path: "/a", Ingress: passthroughIngress}}, "nil Egress"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			known, err := validateRoutes(tc.routes)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				// known must contain the built-ins plus every custom path.
				for p := range defaultKnownPaths {
					if !known[p] {
						t.Errorf("known missing built-in %q", p)
					}
				}
				for _, route := range tc.routes {
					if !known[route.Path] {
						t.Errorf("known missing custom route %q", route.Path)
					}
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestNewServer_EmptyConfigPath(t *testing.T) {
	if _, err := NewServer(Config{}); err == nil {
		t.Fatal("expected error for empty ConfigPath")
	}
}

func TestNewServer_ExecuteAndClose(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	path := writeTempConfig(t, minimalConfig(t, nil))
	s, err := NewServer(Config{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}

	result, err := s.Execute(context.Background(), &pine.Request{Common: map[string]any{"x": 42.0}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := result.Common["y"]; got != 42.0 {
		t.Errorf("y = %v, want 42", got)
	}

	s.Close()

	// After Close no snapshot is live: Execute fails, Acquire returns nil.
	if _, err := s.Execute(context.Background(), &pine.Request{Common: map[string]any{"x": 1.0}}); !errors.Is(err, ErrEngineNotLoaded) {
		t.Errorf("Execute after Close = %v, want ErrEngineNotLoaded", err)
	}
	if h := s.Acquire(); h != nil {
		h.Release()
		t.Error("Acquire after Close should return nil")
	}
}

func TestNewServer_AcquireHandle(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	path := writeTempConfig(t, minimalConfig(t, nil))
	s, err := NewServer(Config{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	h := s.Acquire()
	if h == nil {
		t.Fatal("expected a live Handle")
		return // unreachable, staticcheck SA5011 terminator
	}
	if h.Engine() == nil {
		t.Error("Handle.Engine should not be nil")
	}
	if h.Resources() == nil {
		t.Error("Handle.Resources should not be nil")
	}

	// The Handle keeps the snapshot alive: Execute via the held engine works.
	out, err := h.Engine().Execute(
		resource.WithResources(context.Background(), h.Resources()),
		&pine.Request{Common: map[string]any{"x": 7.0}},
	)
	if err != nil {
		t.Fatalf("Execute via Handle: %v", err)
	}
	if got := out.Common["y"]; got != 7.0 {
		t.Errorf("y = %v, want 7", got)
	}
	h.Release()
}

func TestNewServer_WatchToggle(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	path := writeTempConfig(t, minimalConfig(t, nil))

	// Default (nil) starts the watcher for backward compatibility.
	def, err := NewServer(Config{ConfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if def.watchCancel == nil || def.watchDone == nil {
		t.Error("Watch=nil should start the config watcher")
	}
	def.Close()

	// Explicit true also starts it.
	on, err := NewServer(Config{ConfigPath: path, Watch: pine.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	if on.watchCancel == nil {
		t.Error("Watch=true should start the config watcher")
	}
	on.Close()

	// Explicit false disables it.
	off, err := NewServer(Config{ConfigPath: path, Watch: pine.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	if off.watchCancel != nil || off.watchDone != nil {
		t.Error("Watch=false should not start the config watcher")
	}
	off.Close()
}

func TestRouteHandler_MethodEnforcement(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	path := writeTempConfig(t, minimalConfig(t, nil))
	s, err := NewServer(Config{ConfigPath: path, Watch: pine.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	handler := s.routeHandler(Route{
		Method:  http.MethodPost,
		Path:    "/api/echo",
		Ingress: passthroughIngress,
		Egress:  discardEgress,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/echo", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
	if !strings.Contains(w.Body.String(), "method not allowed") {
		t.Errorf("body = %q, want method not allowed", w.Body.String())
	}
}

func TestRouteHandler_IngressErrorReachesEgress(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	path := writeTempConfig(t, minimalConfig(t, nil))
	s, err := NewServer(Config{ConfigPath: path, Watch: pine.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ingressErr := errors.New("bad payload")
	var gotResult *pine.Result
	var gotErr error
	handler := s.routeHandler(Route{
		Path: "/api/fail",
		Ingress: func(r *http.Request) (*pine.Request, error) {
			return nil, ingressErr
		},
		Egress: func(w http.ResponseWriter, r *http.Request, result *pine.Result, err error) {
			gotResult, gotErr = result, err
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/fail", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !errors.Is(gotErr, ingressErr) {
		t.Errorf("Egress err = %v, want ingress error", gotErr)
	}
	if gotResult != nil {
		t.Errorf("Egress result = %v, want nil on ingress error", gotResult)
	}
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (written by Egress)", w.Code)
	}
}

func TestRouteHandler_ExecutePipeline(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	path := writeTempConfig(t, minimalConfig(t, nil))
	s, err := NewServer(Config{ConfigPath: path, Watch: pine.Bool(false)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	handler := s.routeHandler(Route{
		Method: http.MethodPost,
		Path:   "/api/echo",
		Ingress: func(r *http.Request) (*pine.Request, error) {
			return &pine.Request{Common: map[string]any{"x": 9.0}}, nil
		},
		Egress: func(w http.ResponseWriter, r *http.Request, result *pine.Result, err error) {
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"y": result.Common["y"]})
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/echo", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"y":9`) {
		t.Errorf("body = %q, want y=9 from pipeline", w.Body.String())
	}
}

// Custom routes must not bypass MaxRequestBodySize: the server caps the body
// before user Ingress code runs, and an oversized body gets the same central
// 413 response as the built-in /execute — Egress is never invoked.
func TestRouteHandler_BodyLimitEnforced(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	path := writeTempConfig(t, minimalConfig(t, nil))
	s, err := NewServer(Config{
		ConfigPath:         path,
		Watch:              pine.Bool(false),
		MaxRequestBodySize: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	egressCalled := false
	handler := s.routeHandler(Route{
		Path: "/api/big",
		Ingress: func(r *http.Request) (*pine.Request, error) {
			if _, err := io.ReadAll(r.Body); err != nil {
				return nil, err
			}
			return &pine.Request{Common: map[string]any{"x": 1.0}}, nil
		},
		Egress: func(w http.ResponseWriter, r *http.Request, result *pine.Result, err error) {
			egressCalled = true
		},
	})

	big := strings.Repeat("a", 4096)
	req := httptest.NewRequest(http.MethodPost, "/api/big", strings.NewReader(big))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
	if !strings.Contains(w.Body.String(), "request body too large") {
		t.Errorf("body = %q, want central 413 message", w.Body.String())
	}
	if egressCalled {
		t.Error("Egress must not run when the body cap trips")
	}
}

// Custom route paths are dispatched by exact string lookup, never as ServeMux
// patterns: a trailing slash must not become a subtree wildcard, and "{}"
// segments must not be interpreted (or panic mux registration).
func TestRun_CustomRoutePathsAreExact(t *testing.T) {
	okEgress := func(w http.ResponseWriter, r *http.Request, result *pine.Result, err error) {
		writeJSON(w, http.StatusOK, map[string]string{"hit": r.URL.Path})
	}
	customRoutes := map[string]http.Handler{}
	for _, p := range []string{"/api/", "/api/{name}"} {
		route := Route{Path: p, Ingress: passthroughIngress, Egress: okEgress}
		// Bypass pipeline execution: the handler under test is only the
		// dispatch layer, so a Server with no snapshot is fine — Egress runs
		// with ErrEngineNotLoaded but still writes 200.
		s := &Server{}
		route.Egress = okEgress
		customRoutes[p] = s.routeHandler(route)
	}
	fallback := fallbackHandler(customRoutes)

	cases := []struct {
		path     string
		wantCode int
	}{
		{"/api/", http.StatusOK},               // exact match
		{"/api/{name}", http.StatusOK},         // literal braces, exact match
		{"/api/anything", http.StatusNotFound}, // no subtree wildcard
		{"/api/sub/deep", http.StatusNotFound},
		{"/api/value", http.StatusNotFound}, // braces are not a wildcard segment
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		w := httptest.NewRecorder()
		fallback.ServeHTTP(w, req)
		if w.Code != tc.wantCode {
			t.Errorf("GET %s = %d, want %d", tc.path, w.Code, tc.wantCode)
		}
	}
}

// Empty Addr must normalize to :8080 in both Run and NewServer — not fall
// through to net/http's ":http" (port 80).
func TestNormalizeConfig_DefaultAddr(t *testing.T) {
	if got := normalizeConfig(Config{}).Addr; got != ":8080" {
		t.Errorf("normalizeConfig empty Addr = %q, want :8080", got)
	}
	if got := normalizeConfig(Config{Addr: ":9999"}).Addr; got != ":9999" {
		t.Errorf("normalizeConfig set Addr = %q, want :9999", got)
	}
}

// NewServer failure after the engine is built must tear down everything built
// so far: with a resource whose second Start fetch fails, the first resource's
// value must be released (its Closer runs) and no watcher goroutine leaks.
func TestNewServer_FailureRollsBackResources(t *testing.T) {
	resource.ResetRegistry()
	defer resource.ResetRegistry()

	// One shared fetcher: first call succeeds with a Closer value, second call
	// fails. Manager.Start iterates the resources map in random order, so a
	// fixed succeed-then-fail sequence keeps the test deterministic: whichever
	// resource loads first must be rolled back when the other one fails.
	closed := make(chan struct{}, 1)
	var calls atomic.Int32
	resource.Register(types.ResourceSchema{
		Name:            "flaky_res",
		Description:     "first fetch succeeds with a Closer, second fails",
		DefaultInterval: 3600,
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			if calls.Add(1) == 1 {
				return closeSignaler{ch: closed}, nil
			}
			return nil, errors.New("connection refused")
		}, nil
	})

	cfg := minimalConfig(t, map[string]any{
		"res_one": map[string]any{"type": "flaky_res", "interval": 3600, "params": map[string]any{}},
		"res_two": map[string]any{"type": "flaky_res", "interval": 3600, "params": map[string]any{}},
	})
	path := writeTempConfig(t, cfg)

	_, err := NewServer(Config{ConfigPath: path, Watch: pine.Bool(false)})
	if err == nil {
		t.Fatal("expected NewServer to fail on the second resource fetch")
	}

	select {
	case <-closed:
		// The first-loaded value was released and closed by the rollback.
	case <-time.After(2 * time.Second):
		t.Error("closable resource value was not closed on NewServer failure")
	}
}

type closeSignaler struct{ ch chan struct{} }

func (c closeSignaler) Close() error {
	select {
	case c.ch <- struct{}{}:
	default:
	}
	return nil
}
