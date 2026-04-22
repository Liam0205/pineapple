package pine

import (
	"context"
	"fmt"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/dag"
	"github.com/Liam0205/pineapple/internal/dataframe"
	"github.com/Liam0205/pineapple/internal/registry"
	"github.com/Liam0205/pineapple/internal/runtime"
	"github.com/Liam0205/pineapple/internal/types"
)

// Re-export Request and Result from internal/types.
type Request = types.Request
type Result = types.Result

// Engine is an immutable, concurrency-safe execution engine.
// Create with NewEngine; call Execute for each request.
type Engine struct {
	plan        *runtime.Plan
	stats       *runtime.Stats
	storageMode dataframe.StorageMode
}

// NewEngine parses a JSON config, validates it, builds the DAG, and returns
// an immutable Engine ready for concurrent Execute calls.
func NewEngine(jsonConfig []byte) (*Engine, error) {
	// 1. Parse config
	cfg, err := config.Load(jsonConfig)
	if err != nil {
		return nil, err
	}

	// 2. Expand operator sequence
	sequence, err := config.ExpandOperatorSequence(cfg)
	if err != nil {
		return nil, err
	}

	// 3. Build operator instances and populate OperatorType
	compiledOps := make([]*runtime.CompiledOperator, len(sequence))
	for i, name := range sequence {
		opCfg := cfg.PipelineConfig.Operators[name]
		op, schema, err := registry.BuildOperator(opCfg.TypeName, opCfg.RawParams)
		if err != nil {
			return nil, err
		}
		// Populate OperatorType from registry schema
		opCfg.OperatorType = string(schema.Type)
		// For backwards compatibility: if recall flag not set in JSON, derive from type
		if schema.Type == types.OpTypeRecall {
			opCfg.Recall = true
		}
		cfg.PipelineConfig.Operators[name] = opCfg
		// If the operator needs metadata, provide it.
		// Filter out the skip (control-flow) field so operators see only
		// business fields — DAG dependency inference still uses the full
		// $metadata.CommonInput that includes the control field.
		if ma, ok := op.(types.MetadataAware); ok {
			commonIn := opCfg.Meta.CommonInput
			if opCfg.Skip != "" {
				commonIn = filterOutField(commonIn, opCfg.Skip)
			}
			ma.SetMetadata(
				commonIn,
				opCfg.Meta.CommonOutput,
				opCfg.Meta.ItemInput,
				opCfg.Meta.ItemOutput,
			)
		}
		// If the operator wants debug info, provide it
		if da, ok := op.(types.DebugAware); ok {
			da.SetDebugInfo(name, opCfg.Debug)
		}
		compiledOps[i] = &runtime.CompiledOperator{
			Name:     name,
			Instance: op,
			Config:   opCfg,
		}
	}

	// 4. Build DAG
	graph, err := dag.Build(sequence, cfg.PipelineConfig.Operators)
	if err != nil {
		return nil, err
	}

	plan := &runtime.Plan{
		Graph:     graph,
		Operators: compiledOps,
		Contract:  cfg.FlowContract,
	}

	return &Engine{plan: plan, stats: runtime.NewStats(), storageMode: dataframe.StorageMode(cfg.StorageMode)}, nil
}

// Execute runs the pipeline for a single request.
func (e *Engine) Execute(ctx context.Context, req *Request) (*Result, error) {
	if req == nil {
		return nil, &ValidationError{Message: "request must not be nil"}
	}
	if req.Common == nil {
		return nil, &ValidationError{Message: "request.Common must not be nil"}
	}

	// Validate common inputs against contract
	for _, field := range e.plan.Contract.CommonInput {
		if _, ok := req.Common[field]; !ok {
			return nil, &ValidationError{
				Message: fmt.Sprintf("missing required common input field %q", field),
			}
		}
	}

	// Validate item inputs if items are provided and contract expects them
	if len(req.Items) > 0 && len(e.plan.Contract.ItemInput) > 0 {
		for i, item := range req.Items {
			for _, field := range e.plan.Contract.ItemInput {
				if _, ok := item[field]; !ok {
					return nil, &ValidationError{
						Message: fmt.Sprintf("item[%d] missing required item input field %q", i, field),
					}
				}
			}
		}
	}

	// Build request-local frame
	frame := dataframe.NewFrame(e.storageMode, req.Common, req.Items)

	// Execute DAG
	warnings, traces, err := runtime.Run(ctx, e.plan, frame, e.stats)

	// Build result — project to declared output fields
	result := dataframe.ToResult(frame, e.plan.Contract.CommonOutput, e.plan.Contract.ItemOutput)
	result.Trace = traces
	for _, w := range warnings {
		result.Warnings = append(result.Warnings, fmt.Errorf("operator %q: %w", w.Operator, w.Err))
	}

	if err != nil {
		return result, err
	}
	return result, nil
}

// Stats returns a point-in-time snapshot of per-operator execution statistics
// accumulated since this Engine was created.
func (e *Engine) Stats() map[string]runtime.OpStatsSnapshot {
	return e.stats.Snapshot()
}

// RenderDAG renders the compiled DAG in the specified format.
// Supported formats: "dot" (Graphviz DOT), "mermaid" (Mermaid flowchart).
func (e *Engine) RenderDAG(format string) (string, error) {
	switch format {
	case "dot":
		return dag.RenderDOT(e.plan.Graph), nil
	case "mermaid":
		return dag.RenderMermaid(e.plan.Graph), nil
	default:
		return "", &ValidationError{Message: fmt.Sprintf("unsupported DAG format %q (use \"dot\" or \"mermaid\")", format)}
	}
}

func filterOutField(ss []string, exclude string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != exclude {
			out = append(out, s)
		}
	}
	return out
}
