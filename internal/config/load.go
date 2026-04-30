package config

import (
	"encoding/json"
	"fmt"
	"strings"

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
		skip, err := normalizeSkip(rawOp)
		if err != nil {
			return nil, &types.ConfigError{Message: fmt.Sprintf("operator %q: %v", name, err)}
		}
		if skip != nil {
			opCfg.Skip = skip
		}
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

// normalizeSkip handles backward compatibility for the skip field.
// It accepts both a single string ("_if_1") and an array (["_if_1"]).
func normalizeSkip(raw json.RawMessage) ([]string, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	skipRaw, ok := obj["skip"]
	if !ok {
		return nil, nil
	}
	var arr []string
	if err := json.Unmarshal(skipRaw, &arr); err == nil {
		return arr, nil
	}
	var s string
	if err := json.Unmarshal(skipRaw, &s); err == nil {
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	}
	return nil, fmt.Errorf("skip must be a string or array of strings")
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

	// Reject ambiguous configs where a name appears as both operator and SubFlow.
	for name := range cfg.PipelineConfig.Operators {
		if _, isSF := cfg.PipelineConfig.PipelineMap[name]; isSF {
			return nil, nil, &types.ConfigError{
				Message: fmt.Sprintf("name %q exists in both operators and pipeline_map", name),
			}
		}
	}

	var sequence []string
	opToSubFlow := make(map[string]string)
	visiting := make(map[string]bool)
	seen := make(map[string]bool)

	if err := expandEntries(cfg, group.Pipeline, "", &sequence, opToSubFlow, visiting, seen); err != nil {
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
	seen map[string]bool,
) error {
	for _, entry := range entries {
		if _, isOp := cfg.PipelineConfig.Operators[entry]; isOp {
			if seen[entry] {
				return &types.ConfigError{
					Message: fmt.Sprintf("operator %q referenced more than once in pipeline tree", entry),
				}
			}
			seen[entry] = true
			*sequence = append(*sequence, entry)
			opToSubFlow[entry] = parentPath
		} else if subFlow, isSF := cfg.PipelineConfig.PipelineMap[entry]; isSF {
			if visiting[entry] {
				return &types.ConfigError{
					Message: fmt.Sprintf("cycle detected in sub-flow expansion: %q", entry),
				}
			}
			visiting[entry] = true
			if err := expandEntries(cfg, subFlow.Pipeline, entry, sequence, opToSubFlow, visiting, seen); err != nil {
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

	// Skip field names must start with '_' (engine-internal control fields)
	for name, op := range cfg.PipelineConfig.Operators {
		for _, skipField := range op.Skip {
			if !strings.HasPrefix(skipField, "_") {
				return &types.ConfigError{
					Message: fmt.Sprintf(
						"operator %q: skip field %q must start with '_' "+
							"(control fields are engine-internal)",
						name, skipField),
				}
			}
		}
	}

	// Skip fields must also appear in $metadata.common_input (DAG ordering)
	for name, op := range cfg.PipelineConfig.Operators {
		for _, skipField := range op.Skip {
			found := false
			for _, ci := range op.Meta.CommonInput {
				if ci == skipField {
					found = true
					break
				}
			}
			if !found {
				return &types.ConfigError{
					Message: fmt.Sprintf(
						"operator %q: skip field %q must also appear in "+
							"$metadata.common_input to ensure correct DAG ordering",
						name, skipField),
				}
			}
		}
	}

	return nil
}
