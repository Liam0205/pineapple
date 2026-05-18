package observe

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func TestLogOpInit(t *testing.T) {
	op := &LogOp{}
	err := op.Init(map[string]any{"log_prefix": "test"})
	if err != nil {
		t.Fatal(err)
	}
	if op.prefix != "test" {
		t.Errorf("prefix = %q, want %q", op.prefix, "test")
	}
}

func TestLogOpInitDefault(t *testing.T) {
	op := &LogOp{}
	err := op.Init(map[string]any{"log_prefix": ""})
	if err != nil {
		t.Fatal(err)
	}
	if op.prefix != "" {
		t.Errorf("prefix = %q, want empty", op.prefix)
	}
}

func TestLogOpSetMetadata(t *testing.T) {
	op := &LogOp{}
	op.SetMetadata([]string{"user_id"}, nil, []string{"item_score"}, nil)
	if len(op.CommonInput) != 1 || op.CommonInput[0] != "user_id" {
		t.Errorf("CommonInput = %v", op.CommonInput)
	}
	if len(op.ItemInput) != 1 || op.ItemInput[0] != "item_score" {
		t.Errorf("ItemInput = %v", op.ItemInput)
	}
}

func TestLogOpExecute(t *testing.T) {
	op := &LogOp{prefix: "test"}
	op.SetMetadata([]string{"user_id"}, nil, []string{"score"}, nil)

	in := pine.NewOperatorInput(
		map[string]any{"user_id": "u1"},
		[]map[string]any{
			{"score": 1.5},
			{"score": 2.0},
		},
	)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}

	// Observe should produce no output
	if cw := out.GetCommonWrites(); len(cw) != 0 {
		t.Errorf("common writes = %v, want empty", cw)
	}
	if ai := out.GetAddedItems(); len(ai) != 0 {
		t.Errorf("added items = %v, want empty", ai)
	}
}

func TestLogOpExecuteEmpty(t *testing.T) {
	op := &LogOp{}
	op.SetMetadata(nil, nil, nil, nil)

	in := pine.NewOperatorInput(map[string]any{}, nil)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
}
