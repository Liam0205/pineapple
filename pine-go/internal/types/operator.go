package types

import (
	"context"
	"fmt"
	"log"
	"sort"

	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
)

// OperatorType represents the semantic type of an operator.
// It determines allowed OperatorOutput methods, DAG dependency rules,
// and Python DSL naming conventions.
type OperatorType string

const (
	OpTypeRecall    OperatorType = "Recall"
	OpTypeTransform OperatorType = "Transform"
	OpTypeFilter    OperatorType = "Filter"
	OpTypeMerge     OperatorType = "Merge"
	OpTypeReorder   OperatorType = "Reorder"
	OpTypeObserve   OperatorType = "Observe"
)

// validOperatorTypes is the set of allowed OperatorType values.
var validOperatorTypes = map[OperatorType]struct{}{
	OpTypeRecall:    {},
	OpTypeTransform: {},
	OpTypeFilter:    {},
	OpTypeMerge:     {},
	OpTypeReorder:   {},
	OpTypeObserve:   {},
}

// AllOperatorTypes lists all operator types in canonical order.
var AllOperatorTypes = []OperatorType{
	OpTypeRecall,
	OpTypeTransform,
	OpTypeFilter,
	OpTypeMerge,
	OpTypeReorder,
	OpTypeObserve,
}

func init() {
	if len(AllOperatorTypes) != len(validOperatorTypes) {
		panic("AllOperatorTypes and validOperatorTypes are out of sync")
	}
}

// IsValidOperatorType returns true if t is a recognised operator type.
func IsValidOperatorType(t OperatorType) bool {
	_, ok := validOperatorTypes[t]
	return ok
}

// ValidateOutput checks that the OperatorOutput only used methods allowed for
// this operator type. Returns an error describing the violation, or nil.
func (t OperatorType) ValidateOutput(out *OperatorOutput) error {
	hasCW := len(out.commonWrites) > 0
	// Whole-column writes are item writes for the purposes of the
	// method-restriction contract: SetItemColumnFloat64 counts as SetItem.
	hasIW := len(out.itemWrites) > 0 || len(out.colWrites) > 0
	hasAI := len(out.addedItems) > 0
	hasRI := len(out.removedItems) > 0
	hasIO := out.itemOrder != nil

	var violations []string

	switch t {
	case OpTypeRecall:
		// Recall may write common (e.g. a recall-generated request id that
		// downstream operators consume): a common write is a normal mutating
		// hazard participant, and the DAG already builds correct RAW/WAW/WAR
		// edges from CommonOutput regardless of operator type. It still must
		// not mutate/remove/reorder existing items — its only item-level
		// action is AddItem (additive).
		if hasIW {
			violations = append(violations, "SetItem")
		}
		if hasRI {
			violations = append(violations, "RemoveItem")
		}
		if hasIO {
			violations = append(violations, "SetItemOrder")
		}
	case OpTypeTransform:
		if hasAI {
			violations = append(violations, "AddItem")
		}
		if hasRI {
			violations = append(violations, "RemoveItem")
		}
		if hasIO {
			violations = append(violations, "SetItemOrder")
		}
	case OpTypeFilter:
		if hasCW {
			violations = append(violations, "SetCommon")
		}
		if hasIW {
			violations = append(violations, "SetItem")
		}
		if hasAI {
			violations = append(violations, "AddItem")
		}
		if hasIO {
			violations = append(violations, "SetItemOrder")
		}
	case OpTypeMerge:
		if hasCW {
			violations = append(violations, "SetCommon")
		}
		if hasAI {
			violations = append(violations, "AddItem")
		}
		if hasIO {
			violations = append(violations, "SetItemOrder")
		}
	case OpTypeReorder:
		if hasCW {
			violations = append(violations, "SetCommon")
		}
		if hasIW {
			violations = append(violations, "SetItem")
		}
		if hasAI {
			violations = append(violations, "AddItem")
		}
		if hasRI {
			violations = append(violations, "RemoveItem")
		}
	case OpTypeObserve:
		if hasCW {
			violations = append(violations, "SetCommon")
		}
		if hasIW {
			violations = append(violations, "SetItem")
		}
		if hasAI {
			violations = append(violations, "AddItem")
		}
		if hasRI {
			violations = append(violations, "RemoveItem")
		}
		if hasIO {
			violations = append(violations, "SetItemOrder")
		}
	}

	if len(violations) > 0 {
		return fmt.Errorf("operator type %s must not call %v", t, violations)
	}
	return nil
}

// ValidateDeclaredOutputs checks that every field the operator wrote is
// declared in its $metadata output lists: common writes against commonOutput,
// and item writes (SetItem, SetItemColumnFloat64, AddItem) against itemOutput.
//
// The DAG's hazard inference is derived entirely from the declared field lists
// (see internal/dag.addEdges), so a write to an undeclared field carries no
// RAW/WAW/WAR edge: nothing orders it against a concurrent writer of the same
// name, and a downstream operator that declares the field as input gets no
// dependency on the producer. Enforcing the declaration turns that silent
// ordering hazard into a deterministic error at the same point where the
// operator-type method restrictions are enforced (see ValidateOutput).
//
// Field names are reported sorted so the message is byte-identical across
// runs and runtimes regardless of map iteration order.
func ValidateDeclaredOutputs(out *OperatorOutput, commonOutput, itemOutput []string) error {
	if undeclared := undeclaredCommonWrites(out, commonOutput); len(undeclared) > 0 {
		return fmt.Errorf("operator wrote undeclared common output field(s) %v", undeclared)
	}
	if undeclared := undeclaredItemWrites(out, itemOutput); len(undeclared) > 0 {
		return fmt.Errorf("operator wrote undeclared item output field(s) %v", undeclared)
	}
	return nil
}

func undeclaredCommonWrites(out *OperatorOutput, commonOutput []string) []string {
	if len(out.commonWrites) == 0 {
		return nil
	}
	declared := fieldSet(commonOutput)
	var undeclared []string
	for field := range out.commonWrites {
		if _, ok := declared[field]; !ok {
			undeclared = append(undeclared, field)
		}
	}
	sort.Strings(undeclared)
	return undeclared
}

func undeclaredItemWrites(out *OperatorOutput, itemOutput []string) []string {
	if len(out.itemWrites) == 0 && len(out.colWrites) == 0 && len(out.addedItems) == 0 {
		return nil
	}
	declared := fieldSet(itemOutput)
	seen := make(map[string]struct{})
	var undeclared []string
	add := func(field string) {
		if _, ok := declared[field]; ok {
			return
		}
		if _, dup := seen[field]; dup {
			return
		}
		seen[field] = struct{}{}
		undeclared = append(undeclared, field)
	}
	for _, w := range out.itemWrites {
		add(w.Field)
	}
	for _, cw := range out.colWrites {
		add(cw.Field)
	}
	for _, item := range out.addedItems {
		for field := range item {
			add(field)
		}
	}
	sort.Strings(undeclared)
	return undeclared
}

func fieldSet(fields []string) map[string]struct{} {
	set := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		set[f] = struct{}{}
	}
	return set
}

// Operator is the interface all operators must implement.
type Operator interface {
	Init(params map[string]any) error
	Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}

// MetadataAware is an optional interface for operators that need access to
// their declared input/output field names from $metadata. The engine calls
// SetMetadata after Init for operators that implement this interface.
type MetadataAware interface {
	SetMetadata(commonInput, commonOutput, itemInput, itemOutput []string)
}

// MetadataHolder stores the four DSL-declared field-name slices and provides
// a default SetMetadata implementation. Embed it in an operator struct to
// satisfy MetadataAware automatically:
//
//	type SortOp struct {
//	    pine.MetadataHolder
//	    ascending bool
//	}
//
// The operator can then access o.CommonInput, o.ItemInput, etc. directly.
type MetadataHolder struct {
	CommonInput  []string
	CommonOutput []string
	ItemInput    []string
	ItemOutput   []string
}

// SetMetadata implements MetadataAware.
func (m *MetadataHolder) SetMetadata(commonInput, commonOutput, itemInput, itemOutput []string) {
	m.CommonInput = commonInput
	m.CommonOutput = commonOutput
	m.ItemInput = itemInput
	m.ItemOutput = itemOutput
}

// DebugAware is an optional interface for operators that need access to
// their debug flag and operator name. The engine calls SetDebugInfo after
// Init for operators that implement this interface.
type DebugAware interface {
	SetDebugInfo(operatorName string, debug bool)
}

// LoggerAware is an optional interface for operators that emit log lines.
// The engine calls SetEngineLogger after Init with its per-engine logger,
// so operator diagnostics carry the owning engine's log_prefix even when
// multiple engines run in one process (issue #172). Embed LoggerHolder to
// satisfy it automatically.
type LoggerAware interface {
	SetEngineLogger(l *log.Logger)
}

// LoggerHolder stores the per-engine logger and provides the Logf helper.
// Embed it in an operator struct to satisfy LoggerAware automatically.
type LoggerHolder struct {
	logger *log.Logger
}

// SetEngineLogger implements LoggerAware.
func (h *LoggerHolder) SetEngineLogger(l *log.Logger) {
	h.logger = l
}

// logOutput emits s through the engine logger (or the process-global one
// before injection / outside an engine, e.g. unit tests). calldepth follows
// the log package convention: 2 reports the caller of the function invoking
// Output, so each wrapper layer above logOutput adds 1. Exported helpers
// that call logOutput directly pass 3 so Lshortfile points at THEIR caller
// (the operator code), not at this file.
func (h *LoggerHolder) logOutput(calldepth int, s string) {
	if h.logger != nil {
		h.logger.Output(calldepth, s) //nolint:errcheck
		return
	}
	log.Output(calldepth, s) //nolint:errcheck
}

// Logf writes a log line through the engine's logger (carrying its
// log_prefix). The reported file:line is Logf's caller.
func (h *LoggerHolder) Logf(format string, args ...any) {
	h.logOutput(3, fmt.Sprintf(format, args...))
}

// OperatorName returns the operator name injected by SetDebugInfo.
func (d *DebugHolder) OperatorName() string {
	return d.operatorName
}

// DebugHolder stores the debug flag and operator name, and provides
// IsDebug / DebugLog helpers. Embed it in an operator struct to satisfy
// DebugAware (and LoggerAware, via the embedded LoggerHolder) automatically:
//
//	type MyOp struct {
//	    pine.MetadataHolder
//	    pine.DebugHolder
//	}
type DebugHolder struct {
	LoggerHolder
	operatorName string
	debug        bool
}

// SetDebugInfo implements DebugAware.
func (d *DebugHolder) SetDebugInfo(operatorName string, debug bool) {
	d.operatorName = operatorName
	d.debug = debug
}

// IsDebug returns whether this operator has debug mode enabled.
func (d *DebugHolder) IsDebug() bool {
	return d.debug
}

// DebugLog prints a log line prefixed with the operator name.
// Only outputs when debug is enabled; silent otherwise.
// The reported file:line is DebugLog's caller.
func (d *DebugHolder) DebugLog(format string, args ...any) {
	if !d.debug {
		return
	}
	msg := fmt.Sprintf(format, args...)
	// Call logOutput directly (not Logf) so calldepth 3 resolves to the
	// operator code invoking DebugLog rather than this file.
	d.logOutput(3, fmt.Sprintf("[pine:debug] operator=%q %s", d.operatorName, msg))
}

// ParamSpec describes a single operator parameter for schema validation.
type ParamSpec struct {
	Type        string // "string", "int64", "float64", "bool", "any"
	Required    bool
	Default     any
	Description string // Human-readable description (required by Register).
	// Templatable opts this param into per-request {{field}} interpolation
	// (issue #74). When true the Apple compiler accepts a templated string
	// value for this param and auto-injects the referenced common fields
	// into the operator's common_input; the engine resolves and coerces the
	// value, attaches the map to OperatorInput, and operators read it via
	// input.TemplatedParam(name). Only string / int64 / float64 / bool
	// scalars are supported.
	Templatable bool
}

// OperatorSchema describes an operator type for registration and validation.
type OperatorSchema struct {
	Name        string
	Type        OperatorType // e.g. OpTypeRecall, OpTypeTransform (required by Register).
	Description string       // One-line summary of the operator (required by Register).
	Params      map[string]ParamSpec
}

// ResourceSchema describes a resource type for registration, codegen,
// and documentation generation. Symmetric with OperatorSchema.
type ResourceSchema struct {
	Name            string               // Resource type name (e.g. "feed_data").
	Description     string               // One-line summary.
	DefaultInterval int                  // Default refresh interval in seconds; 0 → 10min; <0 → never refresh.
	Params          map[string]ParamSpec // Reuses operator ParamSpec.
}

// StatsProvider is an optional interface for operators that expose
// custom runtime statistics to the /stats endpoint.
type StatsProvider interface {
	OperatorStats() map[string]int64
}

// Closer is an optional interface for operators that hold resources needing
// explicit teardown (e.g., a pool of interpreter states). The engine calls
// Close on every operator that implements it when the engine is retired —
// during a config hot-reload, or on shutdown — so a swapped-out engine does
// not leak its operators' resources. Operators without external resources
// simply omit it. Close must be safe to call once; it is not called twice on
// the same instance by the engine.
type Closer interface {
	Close() error
}

// MetricsAware is an optional interface for operators that record
// metrics to an external provider (e.g., Prometheus). The engine calls
// SetMetricsProvider after DebugAware injection for operators that
// implement this interface.
type MetricsAware interface {
	SetMetricsProvider(p metrics.Provider)
}

// ConcurrentSafe is an optional interface. Operators that implement it
// declare their Execute method is safe for concurrent calls on the same
// instance (no mutable receiver state). The engine requires this when
// data_parallel > 1.
type ConcurrentSafe interface {
	IsConcurrentSafe()
}

// ConcurrentSafeMarker is an embeddable struct that satisfies ConcurrentSafe.
// Embed it in an operator struct to declare concurrent safety:
//
//	type MyOp struct {
//	    pine.MetadataHolder
//	    pine.ConcurrentSafeMarker
//	}
type ConcurrentSafeMarker struct{}

// IsConcurrentSafe implements ConcurrentSafe.
func (ConcurrentSafeMarker) IsConcurrentSafe() {}

// ConsumesRowSet marks operators that iterate items and need the row set
// stable before execution. The DAG builder treats them as readers of _row_set_.
type ConsumesRowSet interface {
	consumesRowSet()
}

// ConsumesRowSetMarker is a convenience embed for operators that always consume the row set.
type ConsumesRowSetMarker struct{}

func (ConsumesRowSetMarker) consumesRowSet() {}

// MutatesRowSet marks operators that change which items exist or their order.
// The DAG builder treats them as mutating writers of _row_set_.
type MutatesRowSet interface {
	mutatesRowSet()
}

// MutatesRowSetMarker is a convenience embed for operators that always mutate the row set.
type MutatesRowSetMarker struct{}

func (MutatesRowSetMarker) mutatesRowSet() {}

// AdditiveWritesRowSet marks operators that append new items to the row set
// without reading or modifying existing items. Mutually exclusive with MutatesRowSet.
type AdditiveWritesRowSet interface {
	additiveWritesRowSet()
}

// AdditiveWritesRowSetMarker is a convenience embed for operators that always do additive writes.
type AdditiveWritesRowSetMarker struct{}

func (AdditiveWritesRowSetMarker) additiveWritesRowSet() {}
