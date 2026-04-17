package pine

import "github.com/Liam0205/pineapple/internal/registry"

// Register registers an operator type with the global registry.
// Typically called from an init() function in the operator's package.
// Panics on duplicate name.
func Register(schema OperatorSchema, factory func() Operator) {
	registry.Register(schema, factory)
}
