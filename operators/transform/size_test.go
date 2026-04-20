package transform

import (
	"context"
	"testing"

	pine "github.com/Liam0205/pineapple"
)

func TestSizeOpExecute(t *testing.T) {
	op := &SizeOp{}
	op.SetMetadata(nil, []string{"item_count"}, nil, nil)
	items := []map[string]any{
		{"id": 1},
		{"id": 2},
		{"id": 3},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["item_count"] != 3 {
		t.Errorf("item_count = %v, want 3", cw["item_count"])
	}
}

func TestSizeOpExecuteEmpty(t *testing.T) {
	op := &SizeOp{}
	op.SetMetadata(nil, []string{"item_count"}, nil, nil)
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	if err := op.Execute(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	cw := out.GetCommonWrites()
	if cw["item_count"] != 0 {
		t.Errorf("item_count = %v, want 0", cw["item_count"])
	}
}
