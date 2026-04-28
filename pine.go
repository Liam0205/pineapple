package pine

import (
	"context"
	"fmt"
	"log"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/dag"
	"github.com/Liam0205/pineapple/internal/dataframe"
	"github.com/Liam0205/pineapple/internal/registry"
	"github.com/Liam0205/pineapple/internal/runtime"
	"github.com/Liam0205/pineapple/internal/types"
	"github.com/Liam0205/pineapple/pkg/metrics"
)

// Re-export Request and Result from internal/types.
type Request = types.Request
type Result = types.Result

// Engine is an immutable, concurrency-safe execution engine.
// Create with NewEngine; call Execute for each request.
type Engine struct {
	plan          *runtime.Plan
	stats         *runtime.Stats
	engineMetrics *runtime.EngineMetrics
	storageMode   dataframe.StorageMode
}

// Option configures optional Engine behaviour.
type Option func(*engineOptions)

type engineOptions struct {
	metricsProvider metrics.Provider
	logPrefix       string
	debug           *bool
}

// WithMetrics configures the Engine to record metrics through the given
// Provider. When omitted, a no-op provider is used (zero overhead).
func WithMetrics(p metrics.Provider) Option {
	return func(o *engineOptions) { o.metricsProvider = p }
}

// WithLogPrefix sets the global log prefix for all log output, including
// third-party operator logs. When omitted, the JSON config's log_prefix
// field is used; when both are set, this Option takes precedence.
func WithLogPrefix(prefix string) Option {
	return func(o *engineOptions) { o.logPrefix = prefix }
}

// WithDebug enables debug snapshot collection for all operators.
// When omitted, the JSON config's root-level debug field is used;
// when both are set, this Option takes precedence.
func WithDebug(debug bool) Option {
	return func(o *engineOptions) { o.debug = &debug }
}

// NewEngine parses a JSON config, validates it, builds the DAG, and returns
// an immutable Engine ready for concurrent Execute calls.
func NewEngine(jsonConfig []byte, opts ...Option) (*Engine, error) {
	var eo engineOptions
	for _, o := range opts {
		o(&eo)
	}
	mp := eo.metricsProvider
	if mp == nil {
		mp = metrics.Nop()
	}
	// 1. Parse config
	cfg, err := config.Load(jsonConfig)
	if err != nil {
		return nil, err
	}

	// 1b. Apply log prefix (Option > JSON config)
	logPrefix := eo.logPrefix
	if logPrefix == "" {
		logPrefix = cfg.LogPrefix
	}
	if logPrefix != "" {
		log.SetPrefix(logPrefix)
		log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	}

	// 1c. Resolve global debug (Option > JSON config)
	globalDebug := cfg.Debug
	if eo.debug != nil {
		globalDebug = *eo.debug
	}

	// 2. Expand operator sequence (with SubFlow membership)
	sequence, opToSubFlow, err := config.ExpandOperatorSequenceWithSubFlows(cfg)
	if err != nil {
		return nil, err
	}

	// 3. Build operator instances and populate OperatorType
	compiledOps := make([]*runtime.CompiledOperator, len(sequence))
	for i, name := range sequence {
		opCfg := cfg.PipelineConfig.Operators[name]
		if globalDebug {
			opCfg.Debug = true
		}
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
		// Normalize and validate data_parallel config
		if err := validateDataParallel(name, &opCfg, schema.Type); err != nil {
			return nil, err
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
		// If the operator records external metrics, inject the provider
		if ma, ok := op.(types.MetricsAware); ok {
			ma.SetMetricsProvider(mp)
		}
		compiledOps[i] = &runtime.CompiledOperator{
			Name:     name,
			Instance: op,
			Config:   opCfg,
		}
	}

	// 4. Build DAG
	graph, err := dag.Build(sequence, cfg.PipelineConfig.Operators, opToSubFlow)
	if err != nil {
		return nil, err
	}

	plan := &runtime.Plan{
		Graph:     graph,
		Operators: compiledOps,
		Contract:  cfg.FlowContract,
	}

	em := runtime.NewEngineMetrics(mp)
	return &Engine{plan: plan, stats: runtime.NewStats(), engineMetrics: em, storageMode: dataframe.StorageMode(cfg.StorageMode)}, nil
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
	warnings, traces, err := runtime.Run(ctx, e.plan, frame, e.stats, e.engineMetrics)

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

// SchedulerStats returns a point-in-time snapshot of scheduler-level statistics.
func (e *Engine) SchedulerStats() runtime.SchedulerStatsSnapshot {
	return e.stats.SchedulerSnapshot()
}

// OperatorCustomStats collects custom statistics from operators that implement
// StatsProvider. Returns nil when no operator reports custom stats.
func (e *Engine) OperatorCustomStats() map[string]map[string]int64 {
	result := make(map[string]map[string]int64)
	for _, cop := range e.plan.Operators {
		if sp, ok := cop.Instance.(types.StatsProvider); ok {
			if s := sp.OperatorStats(); len(s) > 0 {
				result[cop.Name] = s
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// RenderOption configures optional DAG rendering behaviour.
type RenderOption func(*renderOptions)

type renderOptions struct {
	collapse bool
}

// WithCollapse enables SubFlow-level collapse: all operators belonging to the
// same SubFlow are aggregated into a single node in the rendered DAG.
func WithCollapse(collapse bool) RenderOption {
	return func(o *renderOptions) { o.collapse = collapse }
}

// RenderDAG renders the compiled DAG in the specified format.
// Supported formats: "dot" (Graphviz DOT), "mermaid" (Mermaid flowchart).
func (e *Engine) RenderDAG(format string, opts ...RenderOption) (string, error) {
	var ro renderOptions
	for _, o := range opts {
		o(&ro)
	}

	if ro.collapse {
		switch format {
		case "dot":
			return dag.RenderCollapsedDOT(e.plan.Graph), nil
		case "mermaid":
			return dag.RenderCollapsedMermaid(e.plan.Graph), nil
		default:
			return "", &ValidationError{Message: fmt.Sprintf("unsupported DAG format %q (use \"dot\" or \"mermaid\")", format)}
		}
	}

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

func validateDataParallel(opName string, opCfg *config.OperatorConfig, opType types.OperatorType) error {
	if opCfg.DataParallel == 0 {
		opCfg.DataParallel = 1
	}
	if opCfg.DataParallel < 0 {
		return &ValidationError{
			Message: fmt.Sprintf("operator %q: data_parallel must be >= 1, got %d", opName, opCfg.DataParallel),
		}
	}
	if opCfg.DataParallel > 1 {
		if opType != types.OpTypeTransform {
			return &ValidationError{
				Message: fmt.Sprintf("operator %q: data_parallel=%d is only supported for Transform operators, got %s", opName, opCfg.DataParallel, opType),
			}
		}
		if len(opCfg.Meta.CommonOutput) > 0 {
			return &ValidationError{
				Message: fmt.Sprintf("operator %q: data_parallel=%d requires empty $metadata.common_output for Transform operators", opName, opCfg.DataParallel),
			}
		}
		if !isDataParallelSafeTransform(opCfg.TypeName) {
			return &ValidationError{
				Message: fmt.Sprintf("operator %q: data_parallel=%d is not supported for operator type %q because it requires whole-item-set semantics", opName, opCfg.DataParallel, opCfg.TypeName),
			}
		}
	}
	return nil
}

func isDataParallelSafeTransform(typeName string) bool {
	switch typeName {
	case "transform_normalize":
		return false
	default:
		return true
	}
}
