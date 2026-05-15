package codegen

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Liam0205/pineapple/pine-go/internal/registry"
)

// ExportSchemaJSON writes all registered operator schemas as a JSON array
// to the specified path. Java Codegen reads this to generate equivalent bindings.
func ExportSchemaJSON(path string) error {
	schemas := registry.All()
	if len(schemas) == 0 {
		return fmt.Errorf("no operators registered")
	}

	data, err := json.MarshalIndent(schemas, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal schemas: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("exported schema to %s (%d operators)\n", path, len(schemas))
	return nil
}
