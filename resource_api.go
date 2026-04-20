package pine

import "github.com/Liam0205/pineapple/pkg/resource"

// RegisterResource registers a resource type with the global resource registry.
// Typically called from an init() function in the resource's package.
// Panics on duplicate name.
func RegisterResource(schema ResourceSchema, factory resource.FetcherFactory) {
	resource.Register(schema, factory)
}
