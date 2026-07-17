package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
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
