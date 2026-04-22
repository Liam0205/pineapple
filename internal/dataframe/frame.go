package dataframe

import (
	"github.com/Liam0205/pineapple/internal/types"
)

// StorageMode selects the underlying DataFrame storage layout.
type StorageMode string

const (
	StorageModeRow    StorageMode = "row"
	StorageModeColumn StorageMode = "column"
)

// Frame is the interface for request-local DataFrames.
// Not concurrency-safe — the runtime scheduler guards access with a mutex.
type Frame interface {
	Common(field string) any
	SetCommon(field string, value any)
	ItemCount() int
	Item(index int, field string) any

	BuildInput(commonFields, itemFields []string, commonDefaults, itemDefaults map[string]any) *types.OperatorInput
	ApplyOutput(out *types.OperatorOutput, opName string, recall bool) error
	ToResult(commonOut, itemOut []string) *types.Result
}

// NewFrame creates a Frame with the specified storage mode.
// Defaults to row storage for unknown modes.
func NewFrame(mode StorageMode, common map[string]any, items []map[string]any) Frame {
	switch mode {
	case StorageModeColumn:
		return newColumnFrame(common, items)
	default:
		return newRowFrame(common, items)
	}
}

// New creates a row-store Frame (backward-compatible shorthand).
func New(common map[string]any, items []map[string]any) Frame {
	return newRowFrame(common, items)
}

// --- Package-level forwarding functions for backward compatibility ---

func BuildInput(
	f Frame,
	commonFields []string,
	itemFields []string,
	commonDefaults map[string]any,
	itemDefaults map[string]any,
) *types.OperatorInput {
	return f.BuildInput(commonFields, itemFields, commonDefaults, itemDefaults)
}

func ApplyOutput(f Frame, out *types.OperatorOutput, opName string, recall bool) error {
	return f.ApplyOutput(out, opName, recall)
}

func ToResult(f Frame, commonOut, itemOut []string) *types.Result {
	return f.ToResult(commonOut, itemOut)
}
