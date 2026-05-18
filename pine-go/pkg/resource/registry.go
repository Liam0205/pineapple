package resource

import (
	"fmt"
	"sort"
	"sync"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// FetcherFactory creates a Fetcher from config params.
// Business code registers factories in init(), keyed by ResourceSchema.Name.
type FetcherFactory func(params map[string]any) (Fetcher, error)

type registryEntry struct {
	schema  types.ResourceSchema
	factory FetcherFactory
}

var (
	registryMu sync.Mutex
	registry   = make(map[string]registryEntry)
)

// Register registers a ResourceSchema and its FetcherFactory.
// Typically called from init(). Panics on duplicate name.
func Register(schema types.ResourceSchema, factory FetcherFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[schema.Name]; exists {
		panic(fmt.Sprintf("resource: duplicate resource type %q", schema.Name))
	}
	registry[schema.Name] = registryEntry{schema: schema, factory: factory}
}

// All returns all registered ResourceSchemas, sorted by name.
// Used by codegen to generate Python resource classes.
func All() []types.ResourceSchema {
	registryMu.Lock()
	defer registryMu.Unlock()
	schemas := make([]types.ResourceSchema, 0, len(registry))
	for _, e := range registry {
		schemas = append(schemas, e.schema)
	}
	sort.Slice(schemas, func(i, j int) bool {
		return schemas[i].Name < schemas[j].Name
	})
	return schemas
}

// lookupFactory returns the registered factory for a type name, or nil.
func lookupFactory(typeName string) FetcherFactory {
	registryMu.Lock()
	defer registryMu.Unlock()
	e, ok := registry[typeName]
	if !ok {
		return nil
	}
	return e.factory
}

// ResetRegistry clears the global registry. For testing only.
func ResetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]registryEntry)
}
