package runtime

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Liam0205/pineapple/internal/config"
	"github.com/Liam0205/pineapple/internal/dag"
	"github.com/Liam0205/pineapple/internal/dataframe"
	"github.com/Liam0205/pineapple/internal/types"
)

// CompiledOperator holds a built operator instance and its metadata.
type CompiledOperator struct {
	Name     string
	Instance types.Operator
	Config   config.OperatorConfig
}

// Plan is the immutable execution plan compiled at NewEngine time.
type Plan struct {
	Graph     *dag.Graph
	Operators []*CompiledOperator // indexed by dag node index
	Contract  config.FlowContract
}

// Warning records a recoverable warning from an operator.
type Warning struct {
	Operator string
	Err      error
}

// Run executes the DAG plan against a request-local frame.
// Returns collected warnings, per-operator trace, and the first fatal error (if any).
// If stats is non-nil, per-operator execution statistics are accumulated.
func Run(ctx context.Context, plan *Plan, frame *dataframe.Frame, stats *Stats) ([]Warning, []types.OpTrace, error) {
	n := len(plan.Graph.Nodes)
	done := make([]chan struct{}, n)
	for i := 0; i < n; i++ {
		done[i] = make(chan struct{})
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu        sync.Mutex // protects frame access and shared slices
		warnings  []Warning
		traces    = make([]types.OpTrace, n) // pre-allocated, indexed by node
		fatalErr  error
		fatalOnce sync.Once
		wg        sync.WaitGroup
	)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			defer close(done[idx])

			node := plan.Graph.Nodes[idx]
			cop := plan.Operators[idx]

			// Wait for all predecessors
			for _, pred := range node.Preds {
				select {
				case <-done[pred]:
				case <-ctx.Done():
					return
				}
			}

			// Check context before executing
			if ctx.Err() != nil {
				return
			}

			startTime := time.Now()

			// Evaluate skip
			if cop.Config.Skip != "" {
				mu.Lock()
				skipVal := frame.Common(cop.Config.Skip)
				mu.Unlock()
				if skipVal == true {
					traces[idx] = types.OpTrace{
						Name:      cop.Name,
						StartTime: startTime,
						Duration:  time.Since(startTime),
						Skipped:   true,
					}
					if stats != nil {
						stats.RecordSkip(cop.Name)
					}
					return
				}
			}

			// Build input under lock
			mu.Lock()
			input := dataframe.BuildInput(
				frame,
				cop.Config.Meta.CommonInput,
				cop.Config.Meta.ItemInput,
				cop.Config.CommonDefaults,
				cop.Config.ItemDefaults,
			)
			mu.Unlock()

			// Capture input snapshot for debug operators
			var inputSnapshot map[string]any
			if cop.Config.Debug {
				inputSnapshot = snapshotInput(input)
			}

			// Execute operator with panic recovery
			output := types.NewOperatorOutput()
			var execErr error
			func() {
				defer func() {
					if r := recover(); r != nil {
						execErr = &types.PanicError{
							Operator: cop.Name,
							Value:    r,
							Stack:    string(debug.Stack()),
						}
					}
				}()
				execErr = cop.Instance.Execute(ctx, input, output)
			}()

			// Validate output against operator type constraints
			if execErr == nil {
				opType := types.OperatorType(cop.Config.OperatorType)
				if vErr := opType.ValidateOutput(output); vErr != nil {
					execErr = fmt.Errorf("type violation: %w", vErr)
				}
			}

			duration := time.Since(startTime)

			// Handle fatal error
			if execErr != nil {
				traces[idx] = types.OpTrace{
					Name:      cop.Name,
					StartTime: startTime,
					Duration:  duration,
				}
				if stats != nil {
					stats.RecordError(cop.Name, duration)
				}
				fatalOnce.Do(func() {
					if _, ok := execErr.(*types.PanicError); ok {
						fatalErr = execErr
					} else {
						fatalErr = &types.ExecutionError{
							Operator: cop.Name,
							Err:      execErr,
						}
					}
					cancel()
				})
				return
			}

			// Collect warning
			if w := output.GetWarning(); w != nil {
				mu.Lock()
				warnings = append(warnings, Warning{Operator: cop.Name, Err: w})
				mu.Unlock()
			}

			// Capture output snapshot for debug operators
			var outputSnapshot map[string]any
			if cop.Config.Debug {
				outputSnapshot = snapshotOutput(output)
				log.Printf("[pine:debug] operator=%q duration=%v input=%v output=%v",
					cop.Name, duration, inputSnapshot, outputSnapshot)
			}

			// Apply output under lock
			mu.Lock()
			applyErr := dataframe.ApplyOutput(frame, output, cop.Name, cop.Config.Recall)
			mu.Unlock()

			if applyErr != nil {
				fatalOnce.Do(func() {
					fatalErr = &types.ExecutionError{
						Operator: cop.Name,
						Err:      fmt.Errorf("apply output: %w", applyErr),
					}
					cancel()
				})
			}

			// Record trace
			traces[idx] = types.OpTrace{
				Name:           cop.Name,
				StartTime:      startTime,
				Duration:       duration,
				Skipped:        false,
				InputSnapshot:  inputSnapshot,
				OutputSnapshot: outputSnapshot,
			}
			if stats != nil {
				stats.RecordExec(cop.Name, duration)
			}
		}(i)
	}

	wg.Wait()
	return warnings, traces, fatalErr
}

// snapshotInput creates a serializable snapshot of an operator's input.
func snapshotInput(in *types.OperatorInput) map[string]any {
	snap := make(map[string]any)

	// Common fields
	common := make(map[string]any)
	for _, k := range in.CommonKeys() {
		common[k] = in.Common(k)
	}
	if len(common) > 0 {
		snap["common"] = common
	}

	// Item fields
	if in.ItemCount() > 0 {
		items := make([]map[string]any, in.ItemCount())
		for i := 0; i < in.ItemCount(); i++ {
			row := make(map[string]any)
			for _, k := range in.ItemKeys(i) {
				row[k] = in.Item(i, k)
			}
			items[i] = row
		}
		snap["items"] = items
	}

	return snap
}

// snapshotOutput creates a serializable snapshot of an operator's output.
func snapshotOutput(out *types.OperatorOutput) map[string]any {
	snap := make(map[string]any)

	if cw := out.GetCommonWrites(); len(cw) > 0 {
		snap["common_writes"] = cw
	}
	if iw := out.GetItemWrites(); len(iw) > 0 {
		snap["item_writes"] = iw
	}
	if ai := out.GetAddedItems(); len(ai) > 0 {
		snap["added_items"] = ai
	}
	if ri := out.GetRemovedItems(); len(ri) > 0 {
		removed := make([]int, 0, len(ri))
		for idx := range ri {
			removed = append(removed, idx)
		}
		snap["removed_items"] = removed
	}
	if io := out.GetItemOrder(); io != nil {
		snap["item_order"] = io
	}

	return snap
}
