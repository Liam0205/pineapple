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
//   CommonInput:  [<common fields read as scalar globals>]
//   CommonOutput: [<return values from function_for_common>]
//   ItemInput:    [<item fields — scalars in item mode, lists in common mode>]
//   ItemOutput:   [<return values from function_for_item>]
//
// Performance: Lua is ~1.3x slower than native Go for simple operations, scaling
// to ~2x for compute-intensive loops (1000 items). The overhead comes from VM
// interpretation and Go↔Lua type conversion. See design_doc/13_lua_vs_go_benchmark.md.
package lua

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple"
	"github.com/Liam0205/pineapple/pkg/metrics"
	glua "github.com/yuin/gopher-lua"
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
	pool       *statePool
	funcName   string
	isItemMode bool
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

	var err error
	o.pool, err = newStatePool(script)
	if err != nil {
		return fmt.Errorf("lua: failed to load script: %w", err)
	}

	return nil
}

func (o *LuaOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
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

	L := o.pool.Borrow()
	defer o.pool.Return(L)

	if o.isItemMode {
		return o.executeForItem(L, in, out)
	}
	return o.executeForCommon(L, in, out)
}

// executeForItem calls the Lua function once per item.
// Common fields: scalar globals (set once). Item fields: scalar globals (set per item).
// Return values map positionally to itemOutput via SetItem.
func (o *LuaOp) executeForItem(L *glua.LState, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	// Set common globals once
	for _, field := range o.CommonInput {
		L.SetGlobal(field, goToLua(L, in.Common(field)))
	}

	fn := L.GetGlobal(o.funcName)
	if fn == glua.LNil {
		return fmt.Errorf("lua: function %q not found", o.funcName)
	}

	nret := len(o.ItemOutput)
	n := in.ItemCount()

	for i := 0; i < n; i++ {
		// Set item globals for this item
		for _, field := range o.ItemInput {
			L.SetGlobal(field, goToLua(L, in.Item(i, field)))
		}

		if err := L.CallByParam(glua.P{Fn: fn, NRet: nret, Protect: true}); err != nil {
			return fmt.Errorf("lua: item[%d]: %w", i, err)
		}

		// Collect return values (stack has them in order, first return at bottom)
		for j := 0; j < nret; j++ {
			val := luaToGo(L.Get(-(nret - j)))
			out.SetItem(i, o.ItemOutput[j], val)
		}
		L.Pop(nret)
	}

	return nil
}

// executeForCommon calls the Lua function once.
// Common fields: scalar globals. Item fields: Lua table globals (arrays of all items).
// Return values map positionally to commonOutput via SetCommon.
func (o *LuaOp) executeForCommon(L *glua.LState, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	// Set common globals as scalars
	for _, field := range o.CommonInput {
		L.SetGlobal(field, goToLua(L, in.Common(field)))
	}

	// Set item fields as Lua tables (1-indexed arrays)
	n := in.ItemCount()
	for _, field := range o.ItemInput {
		tbl := L.NewTable()
		for i := 0; i < n; i++ {
			tbl.Append(goToLua(L, in.Item(i, field)))
		}
		L.SetGlobal(field, tbl)
	}

	fn := L.GetGlobal(o.funcName)
	if fn == glua.LNil {
		return fmt.Errorf("lua: function %q not found", o.funcName)
	}

	nret := len(o.CommonOutput)
	if err := L.CallByParam(glua.P{Fn: fn, NRet: nret, Protect: true}); err != nil {
		return fmt.Errorf("lua: %w", err)
	}

	// Collect return values positionally
	for j := 0; j < nret; j++ {
		val := luaToGo(L.Get(-(nret - j)))
		out.SetCommon(o.CommonOutput[j], val)
	}
	L.Pop(nret)

	return nil
}

// goToLua converts a Go value to a Lua value.
func goToLua(L *glua.LState, v any) glua.LValue {
	if v == nil {
		return glua.LNil
	}
	switch x := v.(type) {
	case bool:
		return glua.LBool(x)
	case float64:
		return glua.LNumber(x)
	case int64:
		return glua.LNumber(float64(x))
	case int:
		return glua.LNumber(float64(x))
	case string:
		return glua.LString(x)
	default:
		return glua.LString(fmt.Sprintf("%v", x))
	}
}

// luaToGo converts a Lua value to a Go value.
func luaToGo(v glua.LValue) any {
	switch x := v.(type) {
	case *glua.LNilType:
		return nil
	case glua.LBool:
		return bool(x)
	case glua.LNumber:
		return float64(x)
	case glua.LString:
		return string(x)
	default:
		return v.String()
	}
}

// OperatorStats implements pine.StatsProvider.
func (o *LuaOp) OperatorStats() map[string]int64 {
	return o.pool.statsSnapshot()
}

// SetMetricsProvider implements pine.MetricsAware.
func (o *LuaOp) SetMetricsProvider(p metrics.Provider) {
	name := o.OperatorName()
	o.pool.setMetrics(
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
