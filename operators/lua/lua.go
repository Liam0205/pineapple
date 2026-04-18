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
package lua

import (
	"context"
	"fmt"

	pine "github.com/Liam0205/pineapple"
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
	pool       *statePool
	funcName   string
	isItemMode bool

	// Metadata fields, populated via MetadataAware interface
	commonInput  []string
	commonOutput []string
	itemInput    []string
	itemOutput   []string
}

// SetMetadata implements pine.MetadataAware.
func (o *LuaOp) SetMetadata(commonInput, commonOutput, itemInput, itemOutput []string) {
	o.commonInput = commonInput
	o.commonOutput = commonOutput
	o.itemInput = itemInput
	o.itemOutput = itemOutput
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
	for _, field := range o.commonInput {
		L.SetGlobal(field, goToLua(L, in.Common(field)))
	}

	fn := L.GetGlobal(o.funcName)
	if fn == glua.LNil {
		return fmt.Errorf("lua: function %q not found", o.funcName)
	}

	nret := len(o.itemOutput)
	n := in.ItemCount()

	for i := 0; i < n; i++ {
		// Set item globals for this item
		for _, field := range o.itemInput {
			L.SetGlobal(field, goToLua(L, in.Item(i, field)))
		}

		if err := L.CallByParam(glua.P{Fn: fn, NRet: nret, Protect: true}); err != nil {
			return fmt.Errorf("lua: item[%d]: %w", i, err)
		}

		// Collect return values (stack has them in order, first return at bottom)
		for j := 0; j < nret; j++ {
			val := luaToGo(L.Get(-(nret - j)))
			out.SetItem(i, o.itemOutput[j], val)
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
	for _, field := range o.commonInput {
		L.SetGlobal(field, goToLua(L, in.Common(field)))
	}

	// Set item fields as Lua tables (1-indexed arrays)
	n := in.ItemCount()
	for _, field := range o.itemInput {
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

	nret := len(o.commonOutput)
	if err := L.CallByParam(glua.P{Fn: fn, NRet: nret, Protect: true}); err != nil {
		return fmt.Errorf("lua: %w", err)
	}

	// Collect return values positionally
	for j := 0; j < nret; j++ {
		val := luaToGo(L.Get(-(nret - j)))
		out.SetCommon(o.commonOutput[j], val)
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
