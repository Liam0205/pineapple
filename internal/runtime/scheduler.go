package runtime

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"

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
// Returns collected warnings and the first fatal error (if any).
func Run(ctx context.Context, plan *Plan, frame *dataframe.Frame) ([]Warning, error) {
	n := len(plan.Graph.Nodes)
	done := make([]chan struct{}, n)
	for i := 0; i < n; i++ {
		done[i] = make(chan struct{})
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		mu       sync.Mutex // protects frame access
		warnings []Warning
		fatalErr error
		fatalOnce sync.Once
		wg       sync.WaitGroup
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

			// Evaluate skip
			if cop.Config.Skip != "" {
				mu.Lock()
				skipVal := frame.Common(cop.Config.Skip)
				mu.Unlock()
				if skipVal == true {
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

			// Handle fatal error
			if execErr != nil {
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
		}(i)
	}

	wg.Wait()
	return warnings, fatalErr
}
