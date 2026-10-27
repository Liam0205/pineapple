// pine-cancel-probe: embedded-API cancellation parity probe (issue #217).
//
// Runs a fixed set of scenarios through Engine.Execute with a cancelled or
// timing-out context and prints one line per scenario:
//
//	scenario=<name> error=<err or "nil"> common=<sorted common keys>
//
// cross-validate Section 22 runs this probe against the same-shaped probes in
// pine-java (CancelProbe) and pine-cpp (pineapple-cancel-probe) and asserts
// stdout is byte-identical. Each scenario is deterministic: where a cancel
// must land mid-run, an operator issues it itself, so no timing race decides
// which nodes run.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

// cancelHook is set per scenario; cancelSelfOp calls it during Execute.
var cancelHook func()

// cancelSelfOp cancels the request's context from inside the run, ignores
// the cancellation itself and writes common x.
type cancelSelfOp struct{}

func (o *cancelSelfOp) Init(map[string]any) error { return nil }

func (o *cancelSelfOp) Execute(_ context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	cancelHook()
	out.SetCommon("x", true)
	return nil
}

// copyXToYOp writes common y from x.
type copyXToYOp struct{}

func (o *copyXToYOp) Init(map[string]any) error { return nil }

func (o *copyXToYOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	out.SetCommon("y", in.Common("x"))
	return nil
}

const chainConfig = `{
  "pipeline_config": {
    "operators": {
      "op_a": {"type_name": "probe217_cancel_self",
               "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}},
      "op_b": {"type_name": "probe217_copy_x_y",
               "$metadata": {"common_input": ["x"], "common_output": ["y"], "item_input": [], "item_output": []}}
    },
    "pipeline_map": {"s": {"pipeline": ["op_a", "op_b"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["s"]}},
  "flow_contract": {"common_input": [], "common_output": ["x", "y"], "item_input": [], "item_output": []}
}`

const singleConfig = `{
  "pipeline_config": {
    "operators": {
      "op_a": {"type_name": "probe217_cancel_self",
               "$metadata": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}}
    },
    "pipeline_map": {"s": {"pipeline": ["op_a"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["s"]}},
  "flow_contract": {"common_input": [], "common_output": ["x"], "item_input": [], "item_output": []}
}`

const sleepConfig = `{
  "pipeline_config": {
    "operators": {
      "sleep": {"type_name": "transform_bench_sleep", "delay_ms": 10000,
                "$metadata": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}}
    },
    "pipeline_map": {"s": {"pipeline": ["sleep"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["s"]}},
  "flow_contract": {"common_input": [], "common_output": [], "item_input": [], "item_output": []}
}`

func run(name, config string, ctx context.Context) {
	engine, err := pine.NewEngine([]byte(config))
	if err != nil {
		fmt.Printf("scenario=%s FAIL:NewEngine: %v\n", name, err)
		os.Exit(1)
	}
	defer engine.Close()
	res, err := engine.Execute(ctx, &pine.Request{Common: map[string]any{}})
	errText := "nil"
	if err != nil {
		errText = err.Error()
	}
	var keys []string
	if res != nil {
		for k, v := range res.Common {
			if v != nil {
				keys = append(keys, k)
			}
		}
	}
	sort.Strings(keys)
	fmt.Printf("scenario=%s error=%s common=[%s]\n", name, errText, strings.Join(keys, ","))
}

func main() {
	pine.Register(pine.OperatorSchema{Name: "probe217_cancel_self", Type: pine.OpTypeTransform,
		Description: "probe: cancels the request context, then writes x"}, func() pine.Operator { return &cancelSelfOp{} })
	pine.Register(pine.OperatorSchema{Name: "probe217_copy_x_y", Type: pine.OpTypeTransform,
		Description: "probe: copies common x to y"}, func() pine.Operator { return &copyXToYOp{} })

	// A context cancelled before the run: nothing runs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelHook = func() {}
	run("pre_cancel", chainConfig, ctx)

	// op_a cancels mid-run and completes; op_b is skipped.
	ctx, cancel = context.WithCancel(context.Background())
	cancelHook = cancel
	run("cancel_mid_run", chainConfig, ctx)

	// Cancelled while the only node runs: nothing was skipped, so success.
	ctx, cancel = context.WithCancel(context.Background())
	cancelHook = cancel
	run("cancel_last_node", singleConfig, ctx)

	// transform_bench_sleep stops at the deadline and fails itself.
	ctx, cancel = context.WithTimeout(context.Background(), 50*time.Millisecond)
	run("deadline_sleep", sleepConfig, ctx)
	cancel()

	// ... and on a cancel from another goroutine.
	ctx, cancel = context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	run("cancel_sleep", sleepConfig, ctx)
	cancel()
}
