// pine-debug-probe: embedded-API request-level debug parity probe (issue #219).
//
// Runs a fixed set of scenarios through Engine.Execute, attaching (or not) a
// collecting debug.Emitter, and prints per scenario:
//
//	scenario=<name> error=<err or "nil"> items=[<per-item output values>]
//	scenario=<name> event=<operator>/<type>/<subflow>/<kind>/<data>
//
// Event lines are sorted, so concurrent nodes and data_parallel shards cannot
// reorder them. cross-validate Section 23 runs this probe against the
// same-shaped probes in pine-java (DebugProbe) and pine-cpp
// (pineapple-debug-probe) and asserts stdout is byte-identical.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
	"github.com/Liam0205/pineapple/pine-go/pkg/debug"
)

type requestKey struct{}

// emitOp copies item "value" into its configured field and, when a recorder
// is attached, emits the shard's item count and whether the request value
// survived node binding.
type emitOp struct {
	pine.ConcurrentSafeMarker
	field string
}

func (o *emitOp) Init(params map[string]any) error {
	o.field, _ = params["field"].(string)
	return nil
}

func (o *emitOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	n := in.ItemCount()
	if r := debug.FromContext(ctx); r != nil {
		r.Emit("items", n)
		kept := 0
		if ctx.Value(requestKey{}) == "kept" {
			kept = 1
		}
		r.Emit("kept", kept)
	}
	for i := range n {
		out.SetItem(i, o.field, in.Item(i, "value"))
	}
	return nil
}

type collector struct {
	mu    sync.Mutex
	lines []string
}

func (c *collector) Emit(meta debug.NodeMeta, kind string, data any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lines = append(c.lines, fmt.Sprintf("%s/%s/%s/%s/%v", meta.Operator, meta.Type, meta.SubFlow, kind, data))
}

type panicking struct{}

func (panicking) Emit(debug.NodeMeta, string, any) { panic("emitter broke") }

func opJSON(name, field string, dataParallel int) string {
	dp := ""
	if dataParallel > 1 {
		dp = fmt.Sprintf(`, "data_parallel": %d`, dataParallel)
	}
	return fmt.Sprintf(`"%s": {"type_name": "probe219_emit", "field": "%s"%s,
      "$metadata": {"common_input": [], "common_output": [], "item_input": ["value"], "item_output": ["%s"]}}`,
		name, field, dp, field)
}

// nestedConfig: "top" sits directly in the pipeline group, "mid" in SubFlow
// "outer", and "leaf" in SubFlow "inner", which is nested in "outer".
var nestedConfig = `{
  "pipeline_config": {
    "operators": {` + opJSON("top", "a", 1) + `, ` + opJSON("mid", "b", 1) + `, ` + opJSON("leaf", "c", 1) + `},
    "pipeline_map": {"outer": {"pipeline": ["mid", "inner"]}, "inner": {"pipeline": ["leaf"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["top", "outer"]}},
  "flow_contract": {"common_input": [], "common_output": [], "item_input": ["value"], "item_output": ["a", "b", "c"]}
}`

var parallelConfig = `{
  "pipeline_config": {
    "operators": {` + opJSON("rank", "a", 3) + `},
    "pipeline_map": {"scoring": {"pipeline": ["rank"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["scoring"]}},
  "flow_contract": {"common_input": [], "common_output": [], "item_input": ["value"], "item_output": ["a"]}
}`

func run(name, config string, itemCount int, emitter debug.Emitter, c *collector) {
	engine, err := pine.NewEngine([]byte(config))
	if err != nil {
		fmt.Printf("scenario=%s FAIL:NewEngine: %v\n", name, err)
		os.Exit(1)
	}
	defer engine.Close()
	var fields []string
	for _, f := range []string{"a", "b", "c"} {
		if strings.Contains(config, `"field": "`+f+`"`) {
			fields = append(fields, f)
		}
	}
	items := make([]map[string]any, itemCount)
	for i := range items {
		items[i] = map[string]any{"value": i + 1}
	}
	ctx := debug.WithEmitter(context.WithValue(context.Background(), requestKey{}, "kept"), emitter)
	res, err := engine.Execute(ctx, &pine.Request{Common: map[string]any{}, Items: items})
	errText := "nil"
	if err != nil {
		errText = err.Error()
	}
	var values []string
	if res != nil {
		for _, item := range res.Items {
			var vs []string
			for _, f := range fields {
				vs = append(vs, fmt.Sprint(item[f]))
			}
			values = append(values, strings.Join(vs, ":"))
		}
	}
	fmt.Printf("scenario=%s error=%s items=[%s]\n", name, errText, strings.Join(values, ","))
	if c != nil {
		sort.Strings(c.lines)
		for _, line := range c.lines {
			fmt.Printf("scenario=%s event=%s\n", name, line)
		}
	}
}

func main() {
	pine.Register(pine.OperatorSchema{Name: "probe219_emit", Type: pine.OpTypeTransform,
		Description: "probe: copies item value and emits request debug events",
		Params: map[string]pine.ParamSpec{
			"field": {Type: "string", Required: true, Description: "item output field"},
		}}, func() pine.Operator { return &emitOp{} })

	// No emitter: no recorder reaches the operators, results are unchanged.
	run("no_emitter", nestedConfig, 2, nil, nil)

	// NodeMeta carries the operator, its type_name and its direct SubFlow.
	c := &collector{}
	run("node_meta", nestedConfig, 2, c, c)

	// data_parallel: one event per shard, all with the node's meta.
	c = &collector{}
	run("data_parallel", parallelConfig, 6, c, c)

	// A panicking emitter is isolated: the business result is unchanged.
	run("panicking_emitter", parallelConfig, 6, panicking{}, nil)
}
