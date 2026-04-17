package config

// RootConfig is the top-level JSON config structure.
type RootConfig struct {
	PineappleVersion    string                `json:"_PINEAPPLE_VERSION"`
	PineappleCreateTime string                `json:"_PINEAPPLE_CREATE_TIME"`
	PipelineConfig      PipelineConfig        `json:"pipeline_config"`
	PipelineGroup       map[string]SubFlowRef `json:"pipeline_group"`
	FlowContract        FlowContract          `json:"flow_contract"`
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
	Skip             string         `json:"skip,omitempty"`
	Recall           bool           `json:"recall,omitempty"`
	Sources          []string       `json:"sources,omitempty"`
	Debug            bool           `json:"debug,omitempty"`
	CommonDefaults   map[string]any `json:"common_defaults,omitempty"`
	ItemDefaults     map[string]any `json:"item_defaults,omitempty"`
	ForBranchControl bool           `json:"for_branch_control,omitempty"`

	// RawParams holds business parameters (everything except reserved keys).
	// Populated by custom unmarshal.
	RawParams map[string]any `json:"-"`
}
