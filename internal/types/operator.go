package types

import (
	"context"
	"fmt"
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

// IsValidOperatorType returns true if t is a recognised operator type.
func IsValidOperatorType(t OperatorType) bool {
	_, ok := validOperatorTypes[t]
	return ok
}

// IsBarrier returns true if the operator type uses barrier DAG semantics.
func (t OperatorType) IsBarrier() bool {
	return t == OpTypeFilter || t == OpTypeMerge || t == OpTypeReorder
}

// ValidateOutput checks that the OperatorOutput only used methods allowed for
// this operator type. Returns an error describing the violation, or nil.
func (t OperatorType) ValidateOutput(out *OperatorOutput) error {
	hasCW := len(out.commonWrites) > 0
	hasIW := len(out.itemWrites) > 0
	hasAI := len(out.addedItems) > 0
	hasRI := len(out.removedItems) > 0
	hasIO := out.itemOrder != nil

	var violations []string

	switch t {
	case OpTypeRecall:
		if hasCW {
			violations = append(violations, "SetCommon")
		}
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

// ParamSpec describes a single operator parameter for schema validation.
type ParamSpec struct {
	Type        string // "string", "int64", "float64", "bool", "any"
	Required    bool
	Default     any
	Description string // Human-readable description (required by Register).
}

// OperatorSchema describes an operator type for registration and validation.
type OperatorSchema struct {
	Name        string
	Type        OperatorType // e.g. OpTypeRecall, OpTypeTransform (required by Register).
	Description string       // One-line summary of the operator (required by Register).
	Params      map[string]ParamSpec
}
