package resource

import (
	"fmt"
	"sync"
)

// FetcherFactory creates a Fetcher from config params.
// Business code registers factories in init(), keyed by type name.
type FetcherFactory func(params map[string]any) (Fetcher, error)

var (
	registryMu sync.Mutex
	registry   = make(map[string]FetcherFactory)
)

// RegisterFetcher registers a FetcherFactory for the given type name.
// Typically called from init(). Panics on duplicate type name.
func RegisterFetcher(typeName string, factory FetcherFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[typeName]; exists {
		panic(fmt.Sprintf("resource: duplicate fetcher type %q", typeName))
	}
	registry[typeName] = factory
}

// lookupFactory returns the registered factory for a type name, or nil.
func lookupFactory(typeName string) FetcherFactory {
	registryMu.Lock()
	defer registryMu.Unlock()
	return registry[typeName]
}

// resetRegistry clears the global registry. For testing only.
func resetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]FetcherFactory)
}
