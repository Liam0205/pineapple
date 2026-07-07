// Operator: transform_by_lua
// Type: Transform
// Description: Executes a Lua script for per-item or per-common computation.
//
// Params:
//   - lua_script (string, required): Lua source code defining the function to call.
//   - function_for_item (string, optional): Function name to call per item.
//   - function_for_common (string, optional): Function name to call once for all items.
//
// Exactly one of function_for_item or function_for_common must be provided.
//
// Metadata contract (typical usage):
//
//	CommonInput:  [<common fields read as scalar globals>]
//	CommonOutput: [<return values from function_for_common>]
//	ItemInput:    [<item fields — scalars in item mode, lists in common mode>]
//	ItemOutput:   [<return values from function_for_item>]
//
// Performance: Lua is ~1.5x slower than native Go for simple operations,
// scaling to ~2.6x for compute-intensive loops (1000 items, wangshu backend);
// gopher-lua adds another ~30% over wangshu on heavy compute. The overhead
// comes from VM interpretation and Go↔Lua type conversion. See
// design_doc/13_lua_vs_go_benchmark.md for the full 3-way comparison.
//
// Backend: the underlying VM is selected at build time. Default is wangshu
// (https://github.com/Liam0205/wangshu), a pure-Go Lua 5.1 VM; gopher-lua is
// opt-in via `-tags=lua_gopher`. See backend.go for the abstraction.
package lua

import (
	"context"
	"fmt"
	"sync"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_by_lua",
		Type:        pine.OpTypeTransform,
		Description: "Executes a Lua script for per-item or per-common computation.",
		Params: map[string]pine.ParamSpec{
			"lua_script":          {Type: "string", Required: true, Description: "Lua source code defining the function to call."},
			"function_for_item":   {Type: "string", Required: false, Default: "", Description: "Function name to call per item."},
			"function_for_common": {Type: "string", Required: false, Default: "", Description: "Function name to call once for all items."},
		},
	}, func() pine.Operator {
		return &LuaOp{}
	})
}

// LuaOp executes Lua scripts for feature computation or control flow evaluation.
type LuaOp struct {
	pine.MetadataHolder
	pine.DebugHolder
	pine.ConcurrentSafeMarker
	pool       Pool
	funcName   string
	isItemMode bool
	// arrBuf pools the per-ItemInput-field []any buffer used by
	// executeForCommon (#112 finding #3). Without it, the common-mode hot
	// path allocated one make([]any, ItemCount()) per field per request,
	// which at calibrated_itemlua scale (3000 items × ~5 fields × N LuaOps)
	// dominated per-request alloc pressure. The pool stores *[]any so the
	// underlying backing array survives across Put/Get cycles. Safe because
	// Engine.SetGlobal's contract (see backend.go) forbids backends from
	// retaining the caller's []any past return.
	arrBuf sync.Pool
}

func (o *LuaOp) Init(params map[string]any) error {
	script := params["lua_script"].(string)
	funcForItem := params["function_for_item"].(string)
	funcForCommon := params["function_for_common"].(string)

	if funcForItem == "" && funcForCommon == "" {
		return fmt.Errorf("lua: exactly one of function_for_item or function_for_common must be set")
	}
	if funcForItem != "" && funcForCommon != "" {
		return fmt.Errorf("lua: cannot set both function_for_item and function_for_common")
	}

	if funcForItem != "" {
		o.funcName = funcForItem
		o.isItemMode = true
	} else {
		o.funcName = funcForCommon
		o.isItemMode = false
	}

	pool, err := backend.NewPool(script)
	if err != nil {
		return fmt.Errorf("lua: failed to load script: %w", err)
	}
	o.pool = pool

	// Validate that the declared function exists in the script.
	eng := o.pool.Borrow()
	if eng == nil {
		return fmt.Errorf("lua: failed to borrow state for validation")
	}
	if !eng.HasFunction(o.funcName) {
		o.pool.Return(eng)
		return fmt.Errorf("lua: function %q not defined in script", o.funcName)
	}
	o.pool.Return(eng)

	return nil
}

func (o *LuaOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	if o.IsDebug() {
		nonNil := 0
		for _, f := range o.CommonInput {
			if in.Common(f) != nil {
				nonNil++
			}
		}
		o.DebugLog("common_input fields=%d non_nil=%d items=%d mode=%s func=%s",
			len(o.CommonInput), nonNil, in.ItemCount(),
			map[bool]string{true: "item", false: "common"}[o.isItemMode], o.funcName)
	}

	eng := o.pool.Borrow()
	if eng == nil {
		return fmt.Errorf("lua: pool is closed")
	}
	defer o.pool.Return(eng)

	eng.SetContext(ctx)
	defer eng.RemoveContext()

	if o.isItemMode {
		return o.executeForItem(eng, in, out)
	}
	return o.executeForCommon(eng, in, out)
}

// executeForItem calls the Lua function once per item.
// Common fields: scalar globals (set once). Item fields: scalar globals (set per item).
// Return values map positionally to itemOutput.
func (o *LuaOp) executeForItem(eng Engine, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	// Set common globals once
	for _, field := range o.CommonInput {
		if err := eng.SetGlobal(field, in.Common(field)); err != nil {
			return fmt.Errorf("lua: common[%s]: %w", field, err)
		}
	}

	nret := len(o.ItemOutput)
	n := in.ItemCount()
	// Reused across all N items so the per-call []any allocation in Engine.Call
	// is avoided (#112 finding #2). CallInto fills this in place each iteration;
	// values are read into the DataFrame before the next CallInto, so reuse is safe.
	results := make([]any, nret)

	// Hoist per-field columns out of the item loop: one lock + one lookup
	// per field instead of per item × field. The per-item VM boundary
	// (SetGlobal + CallInto) is inherent to item-mode and unchanged.
	cols := make([][]any, len(o.ItemInput))
	for k, field := range o.ItemInput {
		cols[k] = in.ItemColumn(field)
	}

	for i := 0; i < n; i++ {
		// Set item globals for this item
		for k, field := range o.ItemInput {
			if err := eng.SetGlobal(field, cols[k][i]); err != nil {
				return fmt.Errorf("lua: item[%d].%s: %w", i, field, err)
			}
		}

		if _, err := eng.CallInto(o.funcName, results); err != nil {
			return fmt.Errorf("lua: item[%d]: %w", i, err)
		}
		for j := 0; j < nret; j++ {
			out.SetItem(i, o.ItemOutput[j], results[j])
		}
	}

	return nil
}

// executeForCommon calls the Lua function once.
// Common fields: scalar globals. Item fields: list globals (arrays of all items).
// Return values map positionally to commonOutput.
func (o *LuaOp) executeForCommon(eng Engine, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	// Set common globals as scalars
	for _, field := range o.CommonInput {
		if err := eng.SetGlobal(field, in.Common(field)); err != nil {
			return fmt.Errorf("lua: common[%s]: %w", field, err)
		}
	}

	// Set item fields as arrays (one element per item, in order). The backend
	// is responsible for mapping []any to its native sequence container.
	// The []any buffer is pooled across requests via o.arrBuf (#112 finding
	// #3); SetGlobal's contract (backend.go) forbids backends from retaining
	// the caller's slice past return, so reusing the same backing array for
	// the next field — and for the next request — is safe.
	n := in.ItemCount()
	bufPtr, _ := o.arrBuf.Get().(*[]any)
	if bufPtr == nil {
		bufPtr = new([]any)
	}
	defer func() {
		// Clear element references before returning the buffer so the backing
		// array stops pinning the last request's item values into the pool —
		// pool-bounded, so non-leaking, but item-typed payloads (strings,
		// composites) get to be GC'd promptly instead of waiting for the next
		// reuse to overwrite their slot.
		arr := *bufPtr
		for i := range arr {
			arr[i] = nil
		}
		o.arrBuf.Put(bufPtr)
	}()
	for _, field := range o.ItemInput {
		if cap(*bufPtr) < n {
			*bufPtr = make([]any, n)
		} else {
			*bufPtr = (*bufPtr)[:n]
		}
		arr := *bufPtr
		copy(arr, in.ItemColumn(field))
		if err := eng.SetGlobal(field, arr); err != nil {
			return fmt.Errorf("lua: items[].%s: %w", field, err)
		}
	}

	nret := len(o.CommonOutput)
	results, err := eng.Call(o.funcName, nret)
	if err != nil {
		return fmt.Errorf("lua: %w", err)
	}
	for j := 0; j < nret; j++ {
		out.SetCommon(o.CommonOutput[j], results[j])
	}

	return nil
}

// OperatorStats implements pine.StatsProvider.
func (o *LuaOp) OperatorStats() map[string]int64 {
	return o.pool.StatsSnapshot()
}

// Close implements pine.Closer. It marks the state pool closed so the engine
// stops handing out states; idle and in-flight states are then reclaimed by
// the GC once unreferenced. Safe to call on a never-initialized operator.
func (o *LuaOp) Close() error {
	if o.pool != nil {
		o.pool.Close()
	}
	return nil
}

// SetMetricsProvider implements pine.MetricsAware.
func (o *LuaOp) SetMetricsProvider(p metrics.Provider) {
	name := o.OperatorName()
	o.pool.SetMetrics(
		p.NewCounter(metrics.MetricOpts{
			Name: "pine_lua_pool_borrow_total", Help: "Total Lua state borrows.", LabelNames: []string{"operator"},
		}).With(name),
		p.NewCounter(metrics.MetricOpts{
			Name: "pine_lua_pool_return_total", Help: "Total Lua state returns.", LabelNames: []string{"operator"},
		}).With(name),
		p.NewCounter(metrics.MetricOpts{
			Name: "pine_lua_pool_create_total", Help: "Total Lua states created.", LabelNames: []string{"operator"},
		}).With(name),
		p.NewGauge(metrics.MetricOpts{
			Name: "pine_lua_pool_active", Help: "Lua states currently borrowed.", LabelNames: []string{"operator"},
		}).With(name),
	)
}
