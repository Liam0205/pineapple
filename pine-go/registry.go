package pine

import "github.com/Liam0205/pineapple/pine-go/internal/registry"

// Register registers an operator type with the global registry.
// Typically called from an init() function in the operator's package.
// Panics on duplicate name.
func Register(schema OperatorSchema, factory func() Operator) {
	registry.Register(schema, factory)
}

// BuildOperator looks up a registered operator type by name, validates and
// applies parameters, creates an instance, and calls Init.
func BuildOperator(typeName string, params map[string]any) (Operator, OperatorSchema, error) {
	return registry.BuildOperator(typeName, params)
}
