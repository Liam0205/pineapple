// Operator: transform_normalize
// Type: Transform
// Description: Normalizes a numeric item field using min-max scaling to [0, 1].
//
// Params:
//   - field (string, required): Item field to normalize.
//   - output_field (string, optional, default=<field>+"_norm"): Target field for normalized values.
//   - method (string, optional, default="min_max"): Normalization method.
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    [<field>]
//   ItemOutput:   [<output_field>]
package transform

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_normalize",
		Type:        pine.OpTypeTransform,
		Description: "Normalizes a numeric item field using min-max scaling to [0, 1].",
		Params: map[string]pine.ParamSpec{
			"field":        {Type: "string", Required: true, Description: "Item field to normalize."},
			"output_field": {Type: "string", Required: false, Default: "", Description: "Target field for normalized values."},
			"method":       {Type: "string", Required: false, Default: "min_max", Description: "Normalization method."},
		},
	}, func() pine.Operator {
		return &NormalizeOp{}
	})
}

// NormalizeOp applies min-max normalization to an item field.
type NormalizeOp struct {
	field       string
	outputField string
	method      string
}

func (o *NormalizeOp) Init(params map[string]any) error {
	o.field = params["field"].(string)
	o.outputField = params["output_field"].(string)
	if o.outputField == "" {
		o.outputField = o.field + "_norm"
	}
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

	// Collect values
	vals := make([]float64, n)
	for i := 0; i < n; i++ {
		v, err := toFloat64(in.Item(i, o.field))
		if err != nil {
			return fmt.Errorf("transform_normalize: item[%d].%s: %w", i, o.field, err)
		}
		vals[i] = v
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
		out.SetItem(i, o.outputField, norm)
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
