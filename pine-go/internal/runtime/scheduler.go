package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Liam0205/pineapple/pine-go/internal/config"
	"github.com/Liam0205/pineapple/pine-go/internal/dag"
	"github.com/Liam0205/pineapple/pine-go/internal/dataframe"
	"github.com/Liam0205/pineapple/pine-go/internal/types"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
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
// If em is non-nil, metrics are recorded to the external provider.
func Run(ctx context.Context, plan *Plan, frame dataframe.Frame, stats *Stats, em *EngineMetrics) ([]Warning, []types.OpTrace, error) {
	n := len(plan.Graph.Nodes)
	done := make([]chan struct{}, n)
	for i := 0; i < n; i++ {
		done[i] = make(chan struct{})
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	dagStart := time.Now()

	if stats != nil {
		stats.RecordRun()
	}
	if em != nil {
		em.SchedulerRuns.Inc()
	}

	var (
		warningsMu sync.Mutex // protects warnings slice only
		warnings   []Warning
		traces     = make([]types.OpTrace, n) // pre-allocated, indexed by node
		fatalErr   error
		fatalOnce  sync.Once
		wg         sync.WaitGroup
		activeOps  int64 // local concurrency counter
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

			// Evaluate skip — any skip field being truthy causes the operator to be skipped.
			// Standard control operators write bool; truthy check guards against
			// edge cases from hand-written JSON or unexpected Lua return types.
			for _, skipField := range cop.Config.Skip {
				skipVal := frame.Common(skipField)
				if skipVal != nil && skipVal != false {
					traces[idx] = types.OpTrace{
						Name:      cop.Name,
						StartTime: startTime,
						Duration:  time.Since(startTime),
						Skipped:   true,
					}
					if stats != nil {
						stats.RecordSkip(cop.Name)
					}
					if em != nil {
						em.OpSkipTotal.With(cop.Name).Inc()
					}
					return
				}
			}

			// Build input — frame methods are concurrency-safe.
			input, buildErr := dataframe.BuildInput(frame, cop.Name, cop.Config.InputSpec)
			if buildErr != nil {
				fatalOnce.Do(func() {
					fatalErr = &types.ExecutionError{
						Operator: cop.Name,
						Err:      buildErr,
					}
					cancel()
				})
				return
			}

			// Capture input snapshot for debug operators
			var inputSnapshot map[string]any
			if cop.Config.Debug != nil && *cop.Config.Debug {
				inputSnapshot = snapshotInput(input)
			}

			// Execute operator (single or data-parallel)
			current := atomic.AddInt64(&activeOps, 1)
			if stats != nil {
				stats.RecordConcurrency(current)
			}
			if em != nil {
				em.ActiveOps.Add(1)
			}
			var output *types.OperatorOutput
			var execErr error

			if cop.Config.DataParallel > 1 {
				output, execErr = parallelExecute(ctx, cop, input)
			} else {
				output = types.NewOperatorOutput()
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
			}

			// Validate output against operator type constraints
			if execErr == nil {
				opType := types.OperatorType(cop.Config.OperatorType)
				if vErr := opType.ValidateOutput(output); vErr != nil {
					execErr = fmt.Errorf("type violation: %w", vErr)
				}
			}

			duration := time.Since(startTime)
			atomic.AddInt64(&activeOps, -1)
			if em != nil {
				em.ActiveOps.Add(-1)
			}

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
				if em != nil {
					em.OpErrorTotal.With(cop.Name).Inc()
					em.OpExecDuration.With(cop.Name).Observe(metrics.DurationSeconds(duration))
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
				warningsMu.Lock()
				warnings = append(warnings, Warning{Operator: cop.Name, Err: w})
				warningsMu.Unlock()
			}

			// Capture output snapshot for debug operators
			var outputSnapshot map[string]any
			if cop.Config.Debug != nil && *cop.Config.Debug {
				outputSnapshot = snapshotOutput(output)
				inputSize := input.ItemCount()
				outputSize := inputSize + len(output.GetAddedItems()) - len(output.GetRemovedItems())
				inputJSON, err := json.Marshal(inputSnapshot)
				if err != nil {
					inputJSON = []byte(fmt.Sprintf("%v", inputSnapshot))
				}
				outputJSON, err := json.Marshal(outputSnapshot)
				if err != nil {
					outputJSON = []byte(fmt.Sprintf("%v", outputSnapshot))
				}
				log.Printf("[pine-debug] operator=%q duration=%v input_size=%d output_size=%d input=%s output=%s",
					cop.Name, duration, inputSize, outputSize, inputJSON, outputJSON)
			}

			// Apply output — frame methods are concurrency-safe.
			applyErr := dataframe.ApplyOutput(frame, output, cop.Name, cop.Config.Recall)

			if applyErr != nil {
				traces[idx] = types.OpTrace{
					Name:           cop.Name,
					StartTime:      startTime,
					Duration:       duration,
					Skipped:        false,
					InputSnapshot:  inputSnapshot,
					OutputSnapshot: outputSnapshot,
				}
				if stats != nil {
					stats.RecordError(cop.Name, duration)
				}
				if em != nil {
					em.OpErrorTotal.With(cop.Name).Inc()
					em.OpExecDuration.With(cop.Name).Observe(metrics.DurationSeconds(duration))
				}
				fatalOnce.Do(func() {
					// R10-1: ApplyOutput's error already carries the
					// segment prefix (`common write:` / `item[N] write:`
					// / `added item write:` / `SetItemOrder ...`). The
					// earlier `apply output:` wrap added a redundant
					// layer that pine-{cpp,java,python} do not emit,
					// breaking cross-runtime byte-exact error parity.
					// Strip it — engine-layer `pine: execution error in
					// operator "X": ` prefix still applies via the
					// outer ExecutionError String() format.
					fatalErr = &types.ExecutionError{
						Operator: cop.Name,
						Err:      applyErr,
					}
					cancel()
				})
				return
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
			if em != nil {
				em.OpExecTotal.With(cop.Name).Inc()
				em.OpExecDuration.With(cop.Name).Observe(metrics.DurationSeconds(duration))
			}
		}(i)
	}

	wg.Wait()

	var filtered []types.OpTrace
	for _, t := range traces {
		if t.Name != "" {
			filtered = append(filtered, t)
		}
	}

	if em != nil {
		dagDuration := time.Since(dagStart)
		em.DAGExecDuration.Observe(metrics.DurationSeconds(dagDuration))
		if fatalErr != nil {
			em.DAGExecTotal.With("error").Inc()
		} else {
			em.DAGExecTotal.With("success").Inc()
		}
		var executed int
		for _, t := range filtered {
			if !t.Skipped {
				executed++
			}
		}
		em.DAGOpsExecuted.Observe(float64(executed))
	}

	return warnings, filtered, fatalErr
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

	// Item fields — omit if every row is empty (no item_input declared)
	if in.ItemCount() > 0 {
		hasData := false
		items := make([]map[string]any, in.ItemCount())
		for i := 0; i < in.ItemCount(); i++ {
			row := make(map[string]any)
			for _, k := range in.ItemKeys(i) {
				row[k] = in.Item(i, k)
			}
			items[i] = row
			if len(row) > 0 {
				hasData = true
			}
		}
		if hasData {
			snap["items"] = items
		}
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
		iwMap := make(map[int]map[string]any)
		for _, w := range iw {
			if iwMap[w.Index] == nil {
				iwMap[w.Index] = make(map[string]any)
			}
			iwMap[w.Index][w.Field] = w.Value
		}
		snap["item_writes"] = iwMap
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
	return snap
}
