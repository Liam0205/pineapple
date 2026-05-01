package benchmarks

import (
	"context"
	"math"

	pine "github.com/Liam0205/pineapple"
)

// --- L1: identity — pass-through item_input[0] to item_output[0] ---

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "bench_identity",
		Type:        pine.OpTypeTransform,
		Description: "Benchmark: pass-through a single item field.",
	}, func() pine.Operator { return &benchIdentity{} })

	pine.Register(pine.OperatorSchema{
		Name:        "bench_arithmetic",
		Type:        pine.OpTypeTransform,
		Description: "Benchmark: multiply-add on a single item field.",
		Params: map[string]pine.ParamSpec{
			"rate": {Type: "number", Required: true, Description: "Multiplier."},
			"base": {Type: "number", Required: true, Description: "Addend."},
		},
	}, func() pine.Operator { return &benchArithmetic{} })

	pine.Register(pine.OperatorSchema{
		Name:        "bench_branching",
		Type:        pine.OpTypeTransform,
		Description: "Benchmark: tiered discount with if/elseif branches.",
	}, func() pine.Operator { return &benchBranching{} })

	pine.Register(pine.OperatorSchema{
		Name:        "bench_multi_field",
		Type:        pine.OpTypeTransform,
		Description: "Benchmark: weighted score from 3 fields with clamp.",
		Params: map[string]pine.ParamSpec{
			"w1": {Type: "number", Required: true, Description: "Weight for field 1."},
			"w2": {Type: "number", Required: true, Description: "Weight for field 2."},
			"w3": {Type: "number", Required: true, Description: "Weight for field 3."},
		},
	}, func() pine.Operator { return &benchMultiField{} })

	pine.Register(pine.OperatorSchema{
		Name:        "bench_iterative",
		Type:        pine.OpTypeTransform,
		Description: "Benchmark: Horner polynomial evaluation (degree 5).",
	}, func() pine.Operator { return &benchIterative{} })
}

// --- L1: identity ---

type benchIdentity struct {
	pine.MetadataHolder
}

func (o *benchIdentity) Init(_ map[string]any) error { return nil }

func (o *benchIdentity) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	field := o.ItemInput[0]
	outField := o.ItemOutput[0]
	for i := 0; i < in.ItemCount(); i++ {
		out.SetItem(i, outField, in.Item(i, field))
	}
	return nil
}

// --- L2: arithmetic — item_price * rate + base ---

type benchArithmetic struct {
	pine.MetadataHolder
	rate float64
	base float64
}

func (o *benchArithmetic) Init(params map[string]any) error {
	o.rate = params["rate"].(float64)
	o.base = params["base"].(float64)
	return nil
}

func (o *benchArithmetic) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	field := o.ItemInput[0]
	outField := o.ItemOutput[0]
	for i := 0; i < in.ItemCount(); i++ {
		v := in.Item(i, field).(float64)
		out.SetItem(i, outField, v*o.rate+o.base)
	}
	return nil
}

// --- L3: branching — tiered discount based on item_price ---

type benchBranching struct {
	pine.MetadataHolder
}

func (o *benchBranching) Init(_ map[string]any) error { return nil }

func (o *benchBranching) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	field := o.ItemInput[0]
	outField := o.ItemOutput[0]
	for i := 0; i < in.ItemCount(); i++ {
		price := in.Item(i, field).(float64)
		var result float64
		if price > 1000 {
			result = price * 0.7
		} else if price > 500 {
			result = price * 0.8
		} else if price > 100 {
			result = price * 0.9
		} else {
			result = price * 0.95
		}
		out.SetItem(i, outField, result)
	}
	return nil
}

// --- L4: multi-field — weighted score from 3 fields, clamped to [0, 1] ---

type benchMultiField struct {
	pine.MetadataHolder
	w1, w2, w3 float64
}

func (o *benchMultiField) Init(params map[string]any) error {
	o.w1 = params["w1"].(float64)
	o.w2 = params["w2"].(float64)
	o.w3 = params["w3"].(float64)
	return nil
}

func (o *benchMultiField) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	f1, f2, f3 := o.ItemInput[0], o.ItemInput[1], o.ItemInput[2]
	outField := o.ItemOutput[0]
	for i := 0; i < in.ItemCount(); i++ {
		v1 := in.Item(i, f1).(float64)
		v2 := in.Item(i, f2).(float64)
		v3 := in.Item(i, f3).(float64)
		score := o.w1*v1 + o.w2*v2 + o.w3*v3
		score = math.Max(0, math.Min(1, score))
		out.SetItem(i, outField, score)
	}
	return nil
}

// --- L5: iterative — Horner's method polynomial evaluation (degree 5) ---
// p(x) = c0 + c1*x + c2*x^2 + c3*x^3 + c4*x^4 + c5*x^5
// Horner: p(x) = c0 + x*(c1 + x*(c2 + x*(c3 + x*(c4 + x*c5))))

type benchIterative struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
}

func (o *benchIterative) Init(_ map[string]any) error { return nil }

func (o *benchIterative) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	field := o.ItemInput[0]
	outField := o.ItemOutput[0]
	coeffs := [6]float64{1.0, -0.5, 0.25, -0.125, 0.0625, -0.03125}
	for i := 0; i < in.ItemCount(); i++ {
		x := in.Item(i, field).(float64)
		result := coeffs[5]
		for j := 4; j >= 0; j-- {
			result = result*x + coeffs[j]
		}
		out.SetItem(i, outField, result)
	}
	return nil
}
