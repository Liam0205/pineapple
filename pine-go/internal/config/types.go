package config

// RootConfig is the top-level JSON config structure.
type RootConfig struct {
	PineappleVersion    string                   `json:"_PINEAPPLE_VERSION"`
	PineappleCreateTime string                   `json:"_PINEAPPLE_CREATE_TIME"`
	StorageMode         string                   `json:"storage_mode,omitempty"`
	LogPrefix           string                   `json:"log_prefix,omitempty"`
	Debug               bool                     `json:"debug,omitempty"`
	PipelineConfig      PipelineConfig           `json:"pipeline_config"`
	PipelineGroup       map[string]SubFlowRef    `json:"pipeline_group"`
	FlowContract        FlowContract             `json:"flow_contract"`
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
//
// common_input is split into three logical sets (issue #74):
//   - CommonInput: business inputs the operator actually reads.
//   - CommonInputSkip: engine-internal skip control fields (e.g. `_if_*`).
//     Required for DAG ordering so the producing branch op runs first; the
//     operator itself never sees them.
//   - CommonInputTemplate: source fields referenced by `{{field}}` markers
//     in templated params. Resolved against the request frame before
//     execute and surfaced via input.TemplatedParam, not input.Common.
//
// The DAG ranks dependencies against the union of all three. Both the
// _skip and _template lists are optional — absent means an empty set,
// preserving backward compatibility with configs predating issue #74.
type Metadata struct {
	CommonInput         []string `json:"common_input"`
	CommonInputSkip     []string `json:"common_input_skip,omitempty"`
	CommonInputTemplate []string `json:"common_input_template,omitempty"`
	CommonOutput        []string `json:"common_output"`
	ItemInput           []string `json:"item_input"`
	ItemOutput          []string `json:"item_output"`
}

// CommonReadFields returns the union of business / skip / template
// common_input fields in declaration order. Used by the DAG to derive
// per-operator read dependencies. The order is stable: business first,
// then skip, then template, with later entries deduped against earlier.
func (m Metadata) CommonReadFields() []string {
	if len(m.CommonInputSkip) == 0 && len(m.CommonInputTemplate) == 0 {
		return m.CommonInput
	}
	seen := make(map[string]struct{}, len(m.CommonInput)+len(m.CommonInputSkip)+len(m.CommonInputTemplate))
	out := make([]string, 0, len(m.CommonInput)+len(m.CommonInputSkip)+len(m.CommonInputTemplate))
	for _, f := range m.CommonInput {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	for _, f := range m.CommonInputSkip {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	for _, f := range m.CommonInputTemplate {
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

// OperatorConfig holds the parsed config for a single operator instance.
type OperatorConfig struct {
	TypeName             string         `json:"type_name"`
	Meta                 Metadata       `json:"$metadata"`
	CodeInfo             string         `json:"$code_info,omitempty"`
	Skip                 []string       `json:"-"`
	Recall               bool           `json:"recall,omitempty"`
	Sources              []string       `json:"sources,omitempty"`
	Debug                *bool          `json:"debug,omitempty"`
	ConsumesRowSet       bool           `json:"consumes_row_set,omitempty"`
	MutatesRowSet        bool           `json:"mutates_row_set,omitempty"`
	AdditiveWritesRowSet bool           `json:"additive_writes_row_set,omitempty"`
	CommonDefaults       map[string]any `json:"common_defaults,omitempty"`
	ItemDefaults         map[string]any `json:"item_defaults,omitempty"`
	StrictCommon         []string       `json:"strict_common,omitempty"`
	StrictItem           []string       `json:"strict_item,omitempty"`
	ForBranchControl     bool           `json:"for_branch_control,omitempty"`
	DataParallel         int            `json:"data_parallel,omitempty"`

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
	NullableCommon  []string
	StrictItem      []string
	DefaultedItem   []DefaultedField
	NullableItem    []string
}

// ComputeInputFieldSpec creates the InputFieldSpec from metadata, defaults, strict, and skip fields.
// Default mode is Nullable (missing → error, nil → pass through). Strict and Defaulted are opt-in.
func ComputeInputFieldSpec(meta Metadata, commonDefaults, itemDefaults map[string]any, strictCommon, strictItem, skip []string) *InputFieldSpec {
	spec := &InputFieldSpec{}

	// Engine-internal fields hidden from the operator's input view:
	//   * skip control fields (e.g. `_if_*`) — kept in metadata for DAG
	//     ordering only.
	//   * common_input_template source fields (#74) — surfaced via
	//     input.TemplatedParam, not input.Common.
	// Both are unconditionally excluded so old configs (which placed skip
	// fields directly into common_input) and new configs (which carry
	// disjoint bucket lists) produce the same operator-visible input.
	excludeSet := make(map[string]struct{}, len(skip)+len(meta.CommonInputSkip)+len(meta.CommonInputTemplate))
	for _, s := range skip {
		excludeSet[s] = struct{}{}
	}
	for _, s := range meta.CommonInputSkip {
		excludeSet[s] = struct{}{}
	}
	for _, s := range meta.CommonInputTemplate {
		excludeSet[s] = struct{}{}
	}
	strictCommonSet := make(map[string]struct{}, len(strictCommon))
	for _, f := range strictCommon {
		strictCommonSet[f] = struct{}{}
	}
	strictItemSet := make(map[string]struct{}, len(strictItem))
	for _, f := range strictItem {
		strictItemSet[f] = struct{}{}
	}

	for _, field := range meta.CommonInput {
		if _, skipped := excludeSet[field]; skipped {
			continue
		}
		if d, ok := commonDefaults[field]; ok {
			spec.DefaultedCommon = append(spec.DefaultedCommon, DefaultedField{Name: field, Default: d})
		} else if _, strict := strictCommonSet[field]; strict {
			spec.StrictCommon = append(spec.StrictCommon, field)
		} else {
			spec.NullableCommon = append(spec.NullableCommon, field)
		}
	}

	for _, field := range meta.ItemInput {
		if d, ok := itemDefaults[field]; ok {
			spec.DefaultedItem = append(spec.DefaultedItem, DefaultedField{Name: field, Default: d})
		} else if _, strict := strictItemSet[field]; strict {
			spec.StrictItem = append(spec.StrictItem, field)
		} else {
			spec.NullableItem = append(spec.NullableItem, field)
		}
	}

	return spec
}
