package transform

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func startTestServer(t *testing.T, handler http.HandlerFunc) (host string, port int64, cleanup func()) {
	t.Helper()
	srv := httptest.NewServer(handler)
	addr := srv.Listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), int64(addr.Port), srv.Close
}

func TestRemotePineapple_BasicFieldMapping(t *testing.T) {
	host, port, cleanup := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req remoteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if req.Common["age"] == nil {
			t.Error("expected common field 'age'")
		}
		if len(req.Items) != 2 {
			t.Errorf("expected 2 items, got %d", len(req.Items))
		}

		resp := remoteResponse{
			Common: map[string]any{"score": 0.95},
			Items: []map[string]any{
				{"feature": "feat_a"},
				{"feature": "feat_b"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	op := &RemotePineappleOp{}
	if err := op.Init(map[string]any{
		"host":            host,
		"port":            port,
		"allow_private":   true,
		"common_request":  []any{"age"},
		"item_request":    []any{"id"},
		"common_response": []any{"score"},
		"item_response":   []any{"feature"},
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	op.SetMetadata(
		[]string{"user_age"},
		[]string{"user_score"},
		[]string{"item_id"},
		[]string{"item_feature"},
	)

	in := pine.NewOperatorInput(
		map[string]any{"user_age": 25},
		[]map[string]any{{"item_id": "a"}, {"item_id": "b"}},
	)
	out := pine.NewOperatorOutput()

	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cw := out.GetCommonWrites()
	if cw["user_score"] != 0.95 {
		t.Errorf("expected user_score=0.95, got %v", cw["user_score"])
	}

	iw := out.ItemWriteMap()
	if iw[0]["item_feature"] != "feat_a" {
		t.Errorf("item[0] item_feature: want feat_a, got %v", iw[0]["item_feature"])
	}
	if iw[1]["item_feature"] != "feat_b" {
		t.Errorf("item[1] item_feature: want feat_b, got %v", iw[1]["item_feature"])
	}
}

func TestRemotePineapple_NoMapping(t *testing.T) {
	host, port, cleanup := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		var req remoteRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		// Without mapping, field names pass through directly
		if req.Common["user_age"] == nil {
			t.Error("expected common field 'user_age' (no mapping)")
		}

		resp := remoteResponse{
			Common: map[string]any{"user_score": 0.8},
			Items:  []map[string]any{{"item_feature": "x"}},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	op := &RemotePineappleOp{}
	if err := op.Init(map[string]any{
		"host":          host,
		"port":          port,
		"allow_private": true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	op.SetMetadata(
		[]string{"user_age"},
		[]string{"user_score"},
		nil,
		[]string{"item_feature"},
	)

	in := pine.NewOperatorInput(
		map[string]any{"user_age": 30},
		[]map[string]any{{"item_id": "a"}},
	)
	out := pine.NewOperatorOutput()

	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	cw := out.GetCommonWrites()
	if cw["user_score"] != 0.8 {
		t.Errorf("expected user_score=0.8, got %v", cw["user_score"])
	}
}

func TestRemotePineapple_DownstreamError_Fatal(t *testing.T) {
	host, port, cleanup := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := remoteResponse{Error: "pipeline failed"}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	op := &RemotePineappleOp{}
	if err := op.Init(map[string]any{
		"host":          host,
		"port":          port,
		"allow_private": true,
		"fail_on_error": true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	op.SetMetadata(nil, []string{"out"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{}, nil)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected fatal error from downstream")
	}
}

func TestRemotePineapple_DownstreamError_Warning(t *testing.T) {
	host, port, cleanup := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		resp := remoteResponse{Error: "pipeline failed"}
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer cleanup()

	op := &RemotePineappleOp{}
	if err := op.Init(map[string]any{
		"host":          host,
		"port":          port,
		"allow_private": true,
		"fail_on_error": false,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	op.SetMetadata(nil, []string{"out"}, nil, nil)

	in := pine.NewOperatorInput(map[string]any{}, nil)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatalf("expected nil error (warning mode), got %v", err)
	}
	if out.GetWarning() == nil {
		t.Error("expected warning to be set")
	}
}

func TestRemotePineapple_HTTP500_Fatal(t *testing.T) {
	host, port, cleanup := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", 500)
	})
	defer cleanup()

	op := &RemotePineappleOp{}
	if err := op.Init(map[string]any{
		"host":          host,
		"port":          port,
		"allow_private": true,
		"fail_on_error": true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	op.SetMetadata(nil, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{}, nil)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

func TestRemotePineapple_Timeout(t *testing.T) {
	host, port, cleanup := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		_ = json.NewEncoder(w).Encode(remoteResponse{})
	})
	defer cleanup()

	op := &RemotePineappleOp{}
	if err := op.Init(map[string]any{
		"host":          host,
		"port":          port,
		"allow_private": true,
		"timeout":       0.1,
		"fail_on_error": true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	op.SetMetadata(nil, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{}, nil)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRemotePineapple_ResponseTooLarge(t *testing.T) {
	host, port, cleanup := startTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Write a response larger than the configured limit
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Write 2048 bytes (exceeding our 1024 byte limit)
		data := make([]byte, 2048)
		for i := range data {
			data[i] = 'x'
		}
		_, _ = w.Write(data)
	})
	defer cleanup()

	op := &RemotePineappleOp{}
	if err := op.Init(map[string]any{
		"host":              host,
		"port":              port,
		"allow_private":     true,
		"fail_on_error":     true,
		"max_response_size": int64(1024),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	op.SetMetadata(nil, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{}, nil)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error for oversized response")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("expected error about exceeding size limit, got: %v", err)
	}
}

func TestRemotePineapple_SSRFBlocksLoopback(t *testing.T) {
	op := &RemotePineappleOp{}
	err := op.Init(map[string]any{
		"host": "127.0.0.1",
		"port": int64(8080),
	})
	if err == nil {
		t.Fatal("expected Init to return error for loopback address")
	}
	if !strings.Contains(err.Error(), "private") && !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected error about private/not allowed, got: %v", err)
	}
}

func TestRemotePineapple_SSRFBlocksLocalhost(t *testing.T) {
	op := &RemotePineappleOp{}
	err := op.Init(map[string]any{
		"host": "localhost",
		"port": int64(8080),
	})
	if err == nil {
		t.Fatal("expected Init to return error for localhost")
	}
	if !strings.Contains(err.Error(), "private") && !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected error about private/not allowed, got: %v", err)
	}
}

func TestRemotePineapple_AllowPrivateOverride(t *testing.T) {
	op := &RemotePineappleOp{}
	err := op.Init(map[string]any{
		"host":          "127.0.0.1",
		"port":          int64(8080),
		"allow_private": true,
	})
	if err != nil {
		t.Fatalf("expected Init to succeed with allow_private=true, got: %v", err)
	}
}
