package types

import "context"

// Operator is the interface all operators must implement.
type Operator interface {
	Init(params map[string]any) error
	Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}

// ParamSpec describes a single operator parameter for schema validation.
type ParamSpec struct {
	Type     string // "string", "int64", "float64", "bool", "any"
	Required bool
	Default  any
}

// OperatorSchema describes an operator type for registration and validation.
type OperatorSchema struct {
	Name   string
	Params map[string]ParamSpec
}
