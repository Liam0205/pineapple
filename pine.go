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
	plan *runtime.Plan
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

	// 3. Build operator instances
	compiledOps := make([]*runtime.CompiledOperator, len(sequence))
	for i, name := range sequence {
		opCfg := cfg.PipelineConfig.Operators[name]
		op, _, err := registry.BuildOperator(opCfg.TypeName, opCfg.RawParams)
		if err != nil {
			return nil, err
		}
		// If the operator needs metadata, provide it
		if ma, ok := op.(types.MetadataAware); ok {
			ma.SetMetadata(
				opCfg.Meta.CommonInput,
				opCfg.Meta.CommonOutput,
				opCfg.Meta.ItemInput,
				opCfg.Meta.ItemOutput,
			)
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

	return &Engine{plan: plan}, nil
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
	frame := dataframe.New(req.Common, req.Items)

	// Execute DAG
	warnings, err := runtime.Run(ctx, e.plan, frame)

	// Build result
	result := dataframe.ToResult(frame)
	for _, w := range warnings {
		result.Warnings = append(result.Warnings, fmt.Errorf("operator %q: %w", w.Operator, w.Err))
	}

	if err != nil {
		return result, err
	}
	return result, nil
}
