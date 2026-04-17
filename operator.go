package pine

import "github.com/Liam0205/pineapple/internal/types"

// Re-export core types from internal/types so that:
// - External users import "pine" and use pine.Operator, pine.OperatorInput, etc.
// - Internal packages import "internal/types" without creating cycles.

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
