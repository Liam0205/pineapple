package types

import "context"

// Operator is the interface all operators must implement.
type Operator interface {
	Init(params map[string]any) error
	Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}

// MetadataAware is an optional interface for operators that need access to
// their declared input/output field names from $metadata. The engine calls
// SetMetadata after Init for operators that implement this interface.
type MetadataAware interface {
	SetMetadata(commonInput, commonOutput, itemInput, itemOutput []string)
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
