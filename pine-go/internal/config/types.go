package config

// RootConfig is the top-level JSON config structure.
type RootConfig struct {
	PineappleVersion    string                `json:"_PINEAPPLE_VERSION"`
	PineappleCreateTime string                `json:"_PINEAPPLE_CREATE_TIME"`
	StorageMode         string                `json:"storage_mode,omitempty"`
	LogPrefix           string                `json:"log_prefix,omitempty"`
	Debug               bool                  `json:"debug,omitempty"`
	PipelineConfig      PipelineConfig        `json:"pipeline_config"`
	PipelineGroup       map[string]SubFlowRef `json:"pipeline_group"`
	FlowContract        FlowContract          `json:"flow_contract"`
	ResourceConfig      map[string]ResourceEntry `json:"resource_config,omitempty"`
}

// ResourceEntry describes a single resource in the unified config.
type ResourceEntry struct {
	Type     string         `json:"type"`
	Interval int            `json:"interval"`
	Params   map[string]any `json:"params"`
}

// PipelineConfig holds operator definitions and sub-flow definitions.
type PipelineConfig struct {
	Operators   map[string]OperatorConfig `json:"operators"`
	PipelineMap map[string]SubFlowRef     `json:"pipeline_map"`
}

// SubFlowRef references an ordered list of operator or sub-flow names.
type SubFlowRef struct {
	Pipeline []string `json:"pipeline"`
}

// FlowContract declares the input/output fields of a flow.
type FlowContract struct {
	CommonInput  []string `json:"common_input"`
	ItemInput    []string `json:"item_input"`
	CommonOutput []string `json:"common_output"`
	ItemOutput   []string `json:"item_output"`
}

// Metadata declares operator input/output fields for DAG construction.
type Metadata struct {
	CommonInput  []string `json:"common_input"`
	CommonOutput []string `json:"common_output"`
	ItemInput    []string `json:"item_input"`
	ItemOutput   []string `json:"item_output"`
}

// OperatorConfig holds the parsed config for a single operator instance.
type OperatorConfig struct {
	TypeName         string         `json:"type_name"`
	Meta             Metadata       `json:"$metadata"`
	CodeInfo         string         `json:"$code_info,omitempty"`
	Skip             []string       `json:"-"`
	Recall               bool           `json:"recall,omitempty"`
	Sources              []string       `json:"sources,omitempty"`
	Debug                *bool          `json:"debug,omitempty"`
	ConsumesRowSet       bool           `json:"consumes_row_set,omitempty"`
	MutatesRowSet        bool           `json:"mutates_row_set,omitempty"`
	AdditiveWritesRowSet bool           `json:"additive_writes_row_set,omitempty"`
	CommonDefaults   map[string]any `json:"common_defaults,omitempty"`
	ItemDefaults     map[string]any `json:"item_defaults,omitempty"`
	ForBranchControl bool           `json:"for_branch_control,omitempty"`
	DataParallel     int            `json:"data_parallel,omitempty"`

	// OperatorType is populated at engine build time from the registry schema.
	// Not serialized in JSON.
	OperatorType string `json:"-"`

	// RawParams holds business parameters (everything except reserved keys).
	// Populated by custom unmarshal.
	RawParams map[string]any `json:"-"`

	// InputSpec is pre-computed at engine build time from Meta + Defaults + Skip.
	// BuildInput uses this to avoid per-field default lookups at runtime.
	InputSpec *InputFieldSpec `json:"-"`
}

// DefaultedField pairs a field name with its pre-known default value.
type DefaultedField struct {
	Name    string
	Default any
}

// InputFieldSpec separates input fields into strict (error on nil) and
// defaulted (substitute default on nil). Computed once at engine build time.
type InputFieldSpec struct {
	StrictCommon    []string
	DefaultedCommon []DefaultedField
	StrictItem      []string
	DefaultedItem   []DefaultedField
}

// ComputeInputFieldSpec creates the InputFieldSpec from metadata, defaults, and skip fields.
func ComputeInputFieldSpec(meta Metadata, commonDefaults, itemDefaults map[string]any, skip []string) *InputFieldSpec {
	spec := &InputFieldSpec{}

	skipSet := make(map[string]struct{}, len(skip))
	for _, s := range skip {
		skipSet[s] = struct{}{}
	}

	for _, field := range meta.CommonInput {
		if _, skipped := skipSet[field]; skipped {
			continue
		}
		if d, ok := commonDefaults[field]; ok {
			spec.DefaultedCommon = append(spec.DefaultedCommon, DefaultedField{Name: field, Default: d})
		} else {
			spec.StrictCommon = append(spec.StrictCommon, field)
		}
	}

	for _, field := range meta.ItemInput {
		if d, ok := itemDefaults[field]; ok {
			spec.DefaultedItem = append(spec.DefaultedItem, DefaultedField{Name: field, Default: d})
		} else {
			spec.StrictItem = append(spec.StrictItem, field)
		}
	}

	return spec
}
