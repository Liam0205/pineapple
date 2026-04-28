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
// ordered list of operator names. Uses the "main" pipeline group entry.
func ExpandOperatorSequence(cfg *RootConfig) ([]string, error) {
	group, ok := cfg.PipelineGroup["main"]
	if !ok {
		// If only one group exists, use it
		if len(cfg.PipelineGroup) == 1 {
			for _, g := range cfg.PipelineGroup {
				group = g
			}
		} else {
			return nil, &types.ConfigError{Message: "pipeline_group must contain a \"main\" entry or exactly one entry"}
		}
	}

	var sequence []string
	for _, subFlowName := range group.Pipeline {
		subFlow, ok := cfg.PipelineConfig.PipelineMap[subFlowName]
		if !ok {
			return nil, &types.ConfigError{
				Message: fmt.Sprintf("pipeline_group references undefined sub-flow %q", subFlowName),
			}
		}
		for _, opName := range subFlow.Pipeline {
			if _, ok := cfg.PipelineConfig.Operators[opName]; !ok {
				return nil, &types.ConfigError{
					Message: fmt.Sprintf("sub-flow %q references undefined operator %q", subFlowName, opName),
				}
			}
			sequence = append(sequence, opName)
		}
	}

	return sequence, nil
}

// ExpandOperatorSequenceWithSubFlows behaves like ExpandOperatorSequence but
// additionally returns opToSubFlow — a map from operator name to its owning
// sub-flow name. Operators that belong to no named sub-flow map to "".
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
	for _, subFlowName := range group.Pipeline {
		subFlow, ok := cfg.PipelineConfig.PipelineMap[subFlowName]
		if !ok {
			return nil, nil, &types.ConfigError{
				Message: fmt.Sprintf("pipeline_group references undefined sub-flow %q", subFlowName),
			}
		}
		for _, opName := range subFlow.Pipeline {
			if _, ok := cfg.PipelineConfig.Operators[opName]; !ok {
				return nil, nil, &types.ConfigError{
					Message: fmt.Sprintf("sub-flow %q references undefined operator %q", subFlowName, opName),
				}
			}
			sequence = append(sequence, opName)
			opToSubFlow[opName] = subFlowName
		}
	}

	return sequence, opToSubFlow, nil
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
