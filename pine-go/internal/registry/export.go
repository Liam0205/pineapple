package registry

import "github.com/Liam0205/pineapple/pine-go/internal/types"

// All returns all registered operator schemas, sorted by name.
func All() []types.OperatorSchema {
	mu.RLock()
	defer mu.RUnlock()

	// Collect and sort for deterministic output
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sortStrings(names)

	schemas := make([]types.OperatorSchema, len(names))
	for i, name := range names {
		schemas[i] = registry[name].Schema
	}
	return schemas
}

// sortStrings sorts a slice of strings in place (insertion sort — small N).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
