package pine

import "github.com/Liam0205/pineapple/pine-go/internal/types"

// Re-export core types from internal/types so that:
// - External users import "pine" and use pine.Operator, pine.OperatorInput, etc.
// - Internal packages import "internal/types" without creating cycles.

// OperatorType represents the semantic type of an operator.
type OperatorType = types.OperatorType

// Operator type constants.
const (
	OpTypeRecall    = types.OpTypeRecall
	OpTypeTransform = types.OpTypeTransform
	OpTypeFilter    = types.OpTypeFilter
	OpTypeMerge     = types.OpTypeMerge
	OpTypeReorder   = types.OpTypeReorder
	OpTypeObserve   = types.OpTypeObserve
)

// Operator is the interface all operators must implement.
// After Init, the instance is shared across concurrent requests —
// Execute must be safe for concurrent use.
type Operator = types.Operator

// ParamSpec describes a single operator parameter for schema validation.
type ParamSpec = types.ParamSpec

// OperatorSchema describes an operator type for registration and validation.
type OperatorSchema = types.OperatorSchema

// MetadataAware is an optional interface for operators that need access to
// their declared input/output field names from $metadata.
type MetadataAware = types.MetadataAware

// MetadataHolder stores DSL-declared field-name slices and provides a default
// SetMetadata. Embed it in operator structs for automatic MetadataAware compliance.
type MetadataHolder = types.MetadataHolder

// DebugAware is an optional interface for operators that need access to
// their debug flag and operator name.
type DebugAware = types.DebugAware

// DebugHolder stores the debug flag and operator name. Embed it in operator
// structs for automatic DebugAware compliance with IsDebug/DebugLog helpers.
type DebugHolder = types.DebugHolder

// LoggerAware is an optional interface for operators that emit log lines;
// the engine injects its per-engine logger (carrying log_prefix) after Init.
type LoggerAware = types.LoggerAware

// LoggerHolder stores the per-engine logger and provides the Logf helper.
// Embed it in operator structs for automatic LoggerAware compliance.
// DebugHolder embeds it already.
type LoggerHolder = types.LoggerHolder

// ResourceSchema describes a resource type for registration and codegen.
type ResourceSchema = types.ResourceSchema

// StatsProvider is an optional interface for operators that expose
// custom runtime statistics to the /stats endpoint.
type StatsProvider = types.StatsProvider

// Closer is an optional interface for operators that hold resources needing
// explicit teardown. The engine calls Close when the engine is retired
// (config hot-reload or shutdown).
type Closer = types.Closer

// MetricsAware is an optional interface for operators that record
// metrics to an external provider (e.g., Prometheus).
type MetricsAware = types.MetricsAware

// ConcurrentSafe is an optional interface. Operators that implement it
// declare their Execute is safe for concurrent calls on the same instance.
// Required when data_parallel > 1.
type ConcurrentSafe = types.ConcurrentSafe

// ConcurrentSafeMarker is an embeddable struct that satisfies ConcurrentSafe.
type ConcurrentSafeMarker = types.ConcurrentSafeMarker

// ConsumesRowSet marks operators that iterate items and need the row set
// stable before execution. The DAG builder treats them as readers of _row_set_.
type ConsumesRowSet = types.ConsumesRowSet

// ConsumesRowSetMarker is an embeddable struct that satisfies ConsumesRowSet.
type ConsumesRowSetMarker = types.ConsumesRowSetMarker

// MutatesRowSet marks operators that change which items exist or their order.
// The DAG builder treats them as mutating writers of _row_set_.
type MutatesRowSet = types.MutatesRowSet

// MutatesRowSetMarker is an embeddable struct that satisfies MutatesRowSet.
type MutatesRowSetMarker = types.MutatesRowSetMarker

// AdditiveWritesRowSet marks operators that append new items to the row set
// without reading or modifying existing items. Mutually exclusive with MutatesRowSet.
type AdditiveWritesRowSet = types.AdditiveWritesRowSet

// AdditiveWritesRowSetMarker is an embeddable struct that satisfies AdditiveWritesRowSet.
type AdditiveWritesRowSetMarker = types.AdditiveWritesRowSetMarker
