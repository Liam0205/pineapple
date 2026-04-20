package resource

import (
	"encoding/json"
	"fmt"
	"strings"
)

// pipelineJSON is a minimal representation of the pipeline config,
// enough to extract resource_name from operator params.
type pipelineJSON struct {
	PipelineConfig struct {
		Operators map[string]map[string]any `json:"operators"`
	} `json:"pipeline_config"`
}

// ValidateResourceDeps checks that every resource_name referenced in the
// pipeline config is registered in the ResourceManager.
// Call after LoadConfig/Register and Start, before serving traffic.
func ValidateResourceDeps(pipelineConfig []byte, rm *Manager) error {
	var cfg pipelineJSON
	if err := json.Unmarshal(pipelineConfig, &cfg); err != nil {
		return fmt.Errorf("resource: failed to parse pipeline config: %w", err)
	}

	registered := make(map[string]bool)
	for _, name := range rm.Names() {
		registered[name] = true
	}

	var missing []string
	for opKey, params := range cfg.PipelineConfig.Operators {
		resName, ok := params["resource_name"]
		if !ok {
			continue
		}
		name, ok := resName.(string)
		if !ok || name == "" {
			continue
		}
		if !registered[name] {
			typeName, _ := params["type_name"].(string)
			missing = append(missing, fmt.Sprintf("%s (operator %s/%s)", name, typeName, opKey))
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("resource: missing resource definitions: %s", strings.Join(missing, ", "))
	}
	return nil
}
