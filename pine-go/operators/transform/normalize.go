// Operator: transform_normalize
// Type: Transform
// Description: Normalizes a numeric item field using min-max scaling to [0, 1].
//
// Params:
//   - method (string, optional, default="min_max"): Normalization method.
//
// The input field is determined by item_input metadata (first field).
// The output field is determined by item_output metadata (first field).
//
// Metadata contract (typical usage):
//
//	CommonInput:  []
//	CommonOutput: []
//	ItemInput:    [<field>]
//	ItemOutput:   [<output_field>]
package transform

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_normalize",
		Type:        pine.OpTypeTransform,
		Description: "Normalizes a numeric item field using min-max scaling to [0, 1].",
		Params: map[string]pine.ParamSpec{
			"method": {Type: "string", Required: false, Default: "min_max", Description: "Normalization method."},
		},
	}, func() pine.Operator {
		return &NormalizeOp{}
	})
}

// NormalizeOp applies min-max normalization to an item field.
type NormalizeOp struct {
	pine.MetadataHolder
	method string
}

func (o *NormalizeOp) Init(params map[string]any) error {
	o.method = params["method"].(string)
	if o.method != "min_max" {
		return fmt.Errorf("transform_normalize: unsupported method %q", o.method)
	}
	return nil
}

func (o *NormalizeOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	n := in.ItemCount()
	if n == 0 {
		return nil
	}

	field := o.ItemInput[0]
	outputField := o.ItemOutput[0]

	// Collect values. Typed fast path first: when the frame stores the
	// field as a fully-present float64 column, we get the raw array
	// zero-copy and skip per-element boxing + type assertions entirely.
	var vals []float64
	if raw, ok := in.ItemColumnFloat64(field); ok {
		vals = raw
	} else {
		// Batched boxed access: one lock + one lookup instead of
		// per-element Item calls.
		boxed := in.ItemColumn(field)
		vals = make([]float64, n)
		for i, rv := range boxed {
			v, err := toFloat64(rv)
			if err != nil {
				return fmt.Errorf("transform_normalize: item[%d].%s: %w", i, field, err)
			}
			vals[i] = v
		}
	}

	// Find min/max
	minV, maxV := vals[0], vals[0]
	for _, v := range vals[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}

	// Normalize
	rng := maxV - minV
	for i, v := range vals {
		var norm float64
		if rng == 0 {
			norm = 0.0
		} else {
			norm = (v - minV) / rng
		}
		out.SetItem(i, outputField, norm)
	}
	return nil
}

func toFloat64(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int64:
		return float64(x), nil
	case int:
		return float64(x), nil
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", v)
	}
}
