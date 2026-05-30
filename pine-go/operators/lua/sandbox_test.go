package lua

import (
	"context"
	"strings"
	"testing"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
)

// --- Task 1: Sandbox verification tests ---

func TestLuaSandboxBlocksOS(t *testing.T) {
	script := `function f() return os.execute("echo hello") end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"out"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error when calling os.execute in sandbox")
	}
}

func TestLuaSandboxBlocksIO(t *testing.T) {
	script := `function f() return io.open("/etc/passwd", "r") end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"out"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error when calling io.open in sandbox")
	}
}

func TestLuaSandboxBlocksDebug(t *testing.T) {
	script := `function f() return debug.getinfo(1) end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"out"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error when calling debug.getinfo in sandbox")
	}
}

func TestLuaSandboxAllowsMath(t *testing.T) {
	script := `function f() return math.floor(3.7) end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"result"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	writes := out.ItemWriteMap()
	if writes[0]["result"] != 3.0 {
		t.Errorf("expected 3, got %v", writes[0]["result"])
	}
}

func TestLuaSandboxAllowsString(t *testing.T) {
	script := `function f() return string.format("%d", 42) end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"result"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	writes := out.ItemWriteMap()
	if writes[0]["result"] != "42" {
		t.Errorf("expected '42', got %v", writes[0]["result"])
	}
}

func TestLuaSandboxBlocksDofile(t *testing.T) {
	script := `function f() return dofile("/etc/passwd") end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"out"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error when calling dofile in sandbox")
	}
}

func TestLuaSandboxBlocksLoadfile(t *testing.T) {
	script := `function f() return loadfile("/etc/passwd") end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"out"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error when calling loadfile in sandbox")
	}
}

// --- Task 2: Context cancellation test ---

func TestLuaContextCancellation(t *testing.T) {
	script := `function f() while true do end end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, nil, []string{"out"})

	items := []map[string]any{{"x": 1.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- op.Execute(ctx, in, out)
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error from cancelled context")
		}
		if !strings.Contains(err.Error(), "context") {
			t.Errorf("expected error containing 'context', got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return within 5s — possible hang on infinite loop")
	}
}
