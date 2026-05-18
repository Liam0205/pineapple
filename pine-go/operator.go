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

// ResourceSchema describes a resource type for registration and codegen.
type ResourceSchema = types.ResourceSchema

// StatsProvider is an optional interface for operators that expose
// custom runtime statistics to the /stats endpoint.
type StatsProvider = types.StatsProvider

// MetricsAware is an optional interface for operators that record
// metrics to an external provider (e.g., Prometheus).
type MetricsAware = types.MetricsAware

// ConcurrentSafe is an optional interface. Operators that implement it
// declare their Execute is safe for concurrent calls on the same instance.
// Required when data_parallel > 1.
type ConcurrentSafe = types.ConcurrentSafe

// ConcurrentSafeMarker is an embeddable struct that satisfies ConcurrentSafe.
type ConcurrentSafeMarker = types.ConcurrentSafeMarker
