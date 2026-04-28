package config

import (
	"encoding/json"
	"fmt"

	"github.com/Liam0205/pineapple/internal/registry"
	"github.com/Liam0205/pineapple/internal/types"
)

// Load parses a JSON config byte slice into a RootConfig.
func Load(data []byte) (*RootConfig, error) {
	var cfg RootConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, &types.ConfigError{Message: fmt.Sprintf("JSON parse error: %v", err)}
	}

	// Custom-unmarshal operators to extract RawParams
	var raw struct {
		PipelineConfig struct {
			Operators map[string]json.RawMessage `json:"operators"`
		} `json:"pipeline_config"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, &types.ConfigError{Message: fmt.Sprintf("JSON parse error: %v", err)}
	}

	for name, rawOp := range raw.PipelineConfig.Operators {
		opCfg := cfg.PipelineConfig.Operators[name]
		params, err := extractRawParams(rawOp)
		if err != nil {
			return nil, &types.ConfigError{Message: fmt.Sprintf("operator %q: %v", name, err)}
		}
		opCfg.RawParams = params
		cfg.PipelineConfig.Operators[name] = opCfg
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// extractRawParams unmarshals an operator JSON object and returns only
// non-reserved keys as the business parameter map.
func extractRawParams(raw json.RawMessage) (map[string]any, error) {
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	params := make(map[string]any)
	for k, v := range all {
		if !registry.IsReservedKey(k) {
			params[k] = v
		}
	}
	return params, nil
}

// ExpandOperatorSequence expands pipeline_group and pipeline_map into a flat
// ordered list of operator names. Delegates to ExpandOperatorSequenceWithSubFlows.
func ExpandOperatorSequence(cfg *RootConfig) ([]string, error) {
	seq, _, err := ExpandOperatorSequenceWithSubFlows(cfg)
	return seq, err
}

// ExpandOperatorSequenceWithSubFlows recursively expands the pipeline tree into
// a flat operator sequence. Each pipeline entry is either an operator name
// (leaf) or a pipeline_map key (SubFlow, recurse). Returns opToSubFlow mapping
// each operator to its direct parent SubFlow path ("" for top-level operators).
func ExpandOperatorSequenceWithSubFlows(cfg *RootConfig) ([]string, map[string]string, error) {
	group, ok := cfg.PipelineGroup["main"]
	if !ok {
		if len(cfg.PipelineGroup) == 1 {
			for _, g := range cfg.PipelineGroup {
				group = g
			}
		} else {
			return nil, nil, &types.ConfigError{Message: "pipeline_group must contain a \"main\" entry or exactly one entry"}
		}
	}

	var sequence []string
	opToSubFlow := make(map[string]string)
	visiting := make(map[string]bool)

	if err := expandEntries(cfg, group.Pipeline, "", &sequence, opToSubFlow, visiting); err != nil {
		return nil, nil, err
	}

	return sequence, opToSubFlow, nil
}

// expandEntries recursively resolves pipeline entries into leaf operators.
func expandEntries(
	cfg *RootConfig,
	entries []string,
	parentPath string,
	sequence *[]string,
	opToSubFlow map[string]string,
	visiting map[string]bool,
) error {
	for _, entry := range entries {
		if _, isOp := cfg.PipelineConfig.Operators[entry]; isOp {
			*sequence = append(*sequence, entry)
			opToSubFlow[entry] = parentPath
		} else if subFlow, isSF := cfg.PipelineConfig.PipelineMap[entry]; isSF {
			if visiting[entry] {
				return &types.ConfigError{
					Message: fmt.Sprintf("cycle detected in sub-flow expansion: %q", entry),
				}
			}
			visiting[entry] = true
			if err := expandEntries(cfg, subFlow.Pipeline, entry, sequence, opToSubFlow, visiting); err != nil {
				return err
			}
			delete(visiting, entry)
		} else {
			return &types.ConfigError{
				Message: fmt.Sprintf("pipeline entry %q is neither an operator nor a sub-flow", entry),
			}
		}
	}
	return nil
}

// validate checks structural integrity of the config.
func validate(cfg *RootConfig) error {
	if len(cfg.PipelineConfig.Operators) == 0 {
		return &types.ConfigError{Message: "pipeline_config.operators is empty"}
	}
	if len(cfg.PipelineGroup) == 0 {
		return &types.ConfigError{Message: "pipeline_group is empty"}
	}

	// Every operator must have type_name and $metadata
	for name, op := range cfg.PipelineConfig.Operators {
		if op.TypeName == "" {
			return &types.ConfigError{
				Message: fmt.Sprintf("operator %q: missing type_name", name),
			}
		}
	}

	// Every sources reference must point to an existing operator
	for name, op := range cfg.PipelineConfig.Operators {
		for _, src := range op.Sources {
			if _, ok := cfg.PipelineConfig.Operators[src]; !ok {
				return &types.ConfigError{
					Message: fmt.Sprintf("operator %q: sources references undefined operator %q", name, src),
				}
			}
		}
	}

	return nil
}
