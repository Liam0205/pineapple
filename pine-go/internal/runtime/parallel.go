package runtime

import (
	"context"
	"runtime/debug"
	"sync"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

// splitInput divides an OperatorInput's items into n roughly equal shards,
// sharing the same common map. Returns the shard inputs and their start offsets
// within the original items slice.
func splitInput(input *types.OperatorInput, n int) ([]*types.OperatorInput, []int) {
	total := input.ItemCount()
	common := input.RawCommon()
	templated := input.RawTemplated()

	if n <= 1 || total == 0 {
		return []*types.OperatorInput{input}, []int{0}
	}
	if n > total {
		n = total
	}

	base := total / n
	rem := total % n

	parts := make([]*types.OperatorInput, n)
	offsets := make([]int, n)
	start := 0

	if input.IsLazy() {
		baseOffset := input.LazyOffset()
		frame := input.LazyFrame()
		itemDefaults := input.LazyItemDefaults()
		itemFields := input.LazyItemFields()
		for i := 0; i < n; i++ {
			size := base
			if i < rem {
				size++
			}
			parts[i] = types.NewLazyOperatorInput(common, frame, itemDefaults, itemFields, baseOffset+start, size)
			parts[i].SetTemplatedParams(templated)
			offsets[i] = start
			start += size
		}
	} else {
		items := input.RawItems()
		for i := 0; i < n; i++ {
			size := base
			if i < rem {
				size++
			}
			end := start + size
			shardItems := make([]map[string]any, size)
			copy(shardItems, items[start:end])
			parts[i] = types.NewOperatorInput(common, shardItems)
			parts[i].SetTemplatedParams(templated)
			offsets[i] = start
			start = end
		}
	}
	return parts, offsets
}

// mergeOutputs combines shard outputs into a single OperatorOutput by remapping
// item write indices using the provided offsets. Parallel ops are restricted
// to Transform without common_output (enforced by config validation), so
// shards may only emit item_writes, removed_items, and warnings. If any
// shard emits added_items, item_order, or common writes, we panic rather
// than silently drop — that path used to be a silent corruption surface.
func mergeOutputs(opName string, outputs []*types.OperatorOutput, offsets []int) *types.OperatorOutput {
	merged := types.NewOperatorOutput()

	for i, out := range outputs {
		if out == nil {
			continue
		}
		if len(out.GetAddedItems()) > 0 || out.GetItemOrder() != nil ||
			len(out.GetCommonWrites()) > 0 {
			panic(&types.PanicError{
				Operator: opName,
				Value:    "data_parallel shard emitted added_items, item_order, or common writes; only item_writes / removed_items / warnings are allowed",
			})
		}
		if w := out.GetWarning(); w != nil {
			merged.SetWarning(w)
		}
		offset := offsets[i]
		for _, w := range out.GetItemWrites() {
			merged.SetItem(w.Index+offset, w.Field, w.Value)
		}
		for localIdx := range out.GetRemovedItems() {
			merged.RemoveItem(localIdx + offset)
		}
	}
	return merged
}

// parallelExecute splits the input items into DataParallel shards, executes
// the operator concurrently on each shard, and merges the outputs.
func parallelExecute(ctx context.Context, cop *CompiledOperator, input *types.OperatorInput) (*types.OperatorOutput, error) {
	parts, offsets := splitInput(input, cop.Config.DataParallel)

	if len(parts) == 1 {
		out := types.NewOperatorOutput()
		err := executeWithRecovery(ctx, cop, parts[0], out)
		return out, err
	}

	execCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	outputs := make([]*types.OperatorOutput, len(parts))
	var (
		wg      sync.WaitGroup
		errOnce sync.Once
		first   error
	)

	for i := range parts {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			if execCtx.Err() != nil {
				return
			}
			out := types.NewOperatorOutput()
			err := executeWithRecovery(execCtx, cop, parts[idx], out)
			if err != nil {
				errOnce.Do(func() {
					first = err
					cancel()
				})
				return
			}
			outputs[idx] = out
		}(i)
	}

	wg.Wait()
	if first != nil {
		return nil, first
	}
	return mergeOutputs(cop.Name, outputs, offsets), nil
}

// executeWithRecovery runs a single operator Execute with panic recovery.
func executeWithRecovery(ctx context.Context, cop *CompiledOperator, input *types.OperatorInput, output *types.OperatorOutput) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &types.PanicError{
				Operator: cop.Name,
				Value:    r,
				Stack:    string(debug.Stack()),
			}
		}
	}()
	return cop.Instance.Execute(ctx, input, output)
}
