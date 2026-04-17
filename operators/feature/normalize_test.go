package feature

import (
	"context"
	"math"
	"testing"

	pine "github.com/Liam0205/pineapple"
)

func TestNormalizeOpInit(t *testing.T) {
	op := &NormalizeOp{}
	err := op.Init(map[string]any{"field": "score", "output_field": "", "method": "min_max"})
	if err != nil {
		t.Fatal(err)
	}
	if op.outputField != "score_norm" {
		t.Errorf("expected output_field=score_norm, got %s", op.outputField)
	}
}

func TestNormalizeOpInitCustomOutput(t *testing.T) {
	op := &NormalizeOp{}
	err := op.Init(map[string]any{"field": "score", "output_field": "s_norm", "method": "min_max"})
	if err != nil {
		t.Fatal(err)
	}
	if op.outputField != "s_norm" {
		t.Errorf("expected output_field=s_norm, got %s", op.outputField)
	}
}

func TestNormalizeOpInitBadMethod(t *testing.T) {
	op := &NormalizeOp{}
	err := op.Init(map[string]any{"field": "score", "output_field": "", "method": "z_score"})
	if err == nil {
		t.Fatal("expected error for unsupported method")
	}
}

func TestNormalizeOpExecute(t *testing.T) {
	op := &NormalizeOp{field: "score", outputField: "score_norm", method: "min_max"}
	items := []map[string]any{
		{"score": 10.0},
		{"score": 20.0},
		{"score": 30.0},
		{"score": 40.0},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	writes := out.GetItemWrites()
	expected := []float64{0.0, 1.0 / 3.0, 2.0 / 3.0, 1.0}
	for i, exp := range expected {
		got := writes[i]["score_norm"].(float64)
		if math.Abs(got-exp) > 1e-9 {
			t.Errorf("item[%d] score_norm = %f, want %f", i, got, exp)
		}
	}
}

func TestNormalizeOpExecuteEqualValues(t *testing.T) {
	op := &NormalizeOp{field: "score", outputField: "score_norm", method: "min_max"}
	items := []map[string]any{
		{"score": 5.0},
		{"score": 5.0},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)
	writes := out.GetItemWrites()
	for i := 0; i < 2; i++ {
		if writes[i]["score_norm"] != 0.0 {
			t.Errorf("expected 0 for equal values, got %v", writes[i]["score_norm"])
		}
	}
}

func TestNormalizeOpExecuteEmpty(t *testing.T) {
	op := &NormalizeOp{field: "score", outputField: "score_norm", method: "min_max"}
	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeOpExecuteInt64(t *testing.T) {
	op := &NormalizeOp{field: "score", outputField: "score_norm", method: "min_max"}
	items := []map[string]any{
		{"score": int64(0)},
		{"score": int64(100)},
	}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	writes := out.GetItemWrites()
	if writes[0]["score_norm"] != 0.0 || writes[1]["score_norm"] != 1.0 {
		t.Errorf("unexpected normalization: %v, %v", writes[0]["score_norm"], writes[1]["score_norm"])
	}
}

func TestNormalizeOpExecuteBadType(t *testing.T) {
	op := &NormalizeOp{field: "score", outputField: "score_norm", method: "min_max"}
	items := []map[string]any{{"score": "not_a_number"}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err == nil {
		t.Fatal("expected error for non-numeric value")
	}
}
