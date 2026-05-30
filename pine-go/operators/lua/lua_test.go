package lua

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func newLuaOp(t *testing.T, script, funcItem, funcCommon string,
	commonIn, commonOut, itemIn, itemOut []string) *LuaOp {
	t.Helper()
	op := &LuaOp{}
	params := map[string]any{
		"lua_script":          script,
		"function_for_item":   funcItem,
		"function_for_common": funcCommon,
	}
	if err := op.Init(params); err != nil {
		t.Fatal(err)
	}
	op.SetMetadata(commonIn, commonOut, itemIn, itemOut)
	return op
}

func TestLuaOpInitBothFuncs(t *testing.T) {
	op := &LuaOp{}
	err := op.Init(map[string]any{
		"lua_script":          "function a() end",
		"function_for_item":   "a",
		"function_for_common": "a",
	})
	if err == nil {
		t.Fatal("expected error when both functions set")
	}
}

func TestLuaOpInitNoFunc(t *testing.T) {
	op := &LuaOp{}
	err := op.Init(map[string]any{
		"lua_script":          "function a() end",
		"function_for_item":   "",
		"function_for_common": "",
	})
	if err == nil {
		t.Fatal("expected error when no function set")
	}
}

func TestLuaOpInitBadScript(t *testing.T) {
	op := &LuaOp{}
	err := op.Init(map[string]any{
		"lua_script":          "invalid lua {{{{",
		"function_for_item":   "f",
		"function_for_common": "",
	})
	if err == nil {
		t.Fatal("expected error for bad script")
	}
}

func TestLuaOpForItem(t *testing.T) {
	script := `function adjust_price()
		if user_age < 18 then
			return item_price * 0.8
		else
			return item_price
		end
	end`

	op := newLuaOp(t, script, "adjust_price", "",
		[]string{"user_age"}, nil,
		[]string{"item_price"}, []string{"item_adjusted"})

	items := []map[string]any{
		{"item_price": 100.0},
		{"item_price": 200.0},
	}
	in := pine.NewOperatorInput(map[string]any{"user_age": 15.0}, items)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}

	writes := out.ItemWriteMap()
	// user_age < 18 → item_price * 0.8
	if writes[0]["item_adjusted"] != 80.0 {
		t.Errorf("item[0] expected 80, got %v", writes[0]["item_adjusted"])
	}
	if writes[1]["item_adjusted"] != 160.0 {
		t.Errorf("item[1] expected 160, got %v", writes[1]["item_adjusted"])
	}
}

func TestLuaOpForItemAdult(t *testing.T) {
	script := `function adjust_price()
		if user_age < 18 then
			return item_price * 0.8
		else
			return item_price
		end
	end`

	op := newLuaOp(t, script, "adjust_price", "",
		[]string{"user_age"}, nil,
		[]string{"item_price"}, []string{"item_adjusted"})

	items := []map[string]any{{"item_price": 100.0}}
	in := pine.NewOperatorInput(map[string]any{"user_age": 25.0}, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)

	writes := out.ItemWriteMap()
	if writes[0]["item_adjusted"] != 100.0 {
		t.Errorf("expected 100, got %v", writes[0]["item_adjusted"])
	}
}

func TestLuaOpForCommon(t *testing.T) {
	script := `function compute_stats()
		local sum = 0
		local max_val = -math.huge
		for i = 1, #item_price do
			local p = item_price[i] or 0
			sum = sum + p
			if p > max_val then max_val = p end
		end
		return sum / #item_price, max_val
	end`

	op := newLuaOp(t, script, "", "compute_stats",
		nil, []string{"avg_price", "max_price"},
		[]string{"item_price"}, nil)

	items := []map[string]any{
		{"item_price": 100.0},
		{"item_price": 200.0},
		{"item_price": 300.0},
	}
	in := pine.NewOperatorInput(map[string]any{}, items)
	out := pine.NewOperatorOutput()

	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}

	commonWrites := out.GetCommonWrites()
	if commonWrites["avg_price"] != 200.0 {
		t.Errorf("avg_price expected 200, got %v", commonWrites["avg_price"])
	}
	if commonWrites["max_price"] != 300.0 {
		t.Errorf("max_price expected 300, got %v", commonWrites["max_price"])
	}
}

func TestLuaOpForCommonBool(t *testing.T) {
	// Control flow: evaluate condition, return bool
	script := `function evaluate()
		if (item_count > 0) then return false else return true end
	end`

	op := newLuaOp(t, script, "", "evaluate",
		[]string{"item_count"}, []string{"_if_1"},
		nil, nil)

	in := pine.NewOperatorInput(map[string]any{"item_count": 5.0}, nil)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)

	cw := out.GetCommonWrites()
	if cw["_if_1"] != false {
		t.Errorf("expected false (don't skip), got %v", cw["_if_1"])
	}

	// Test with 0 items
	in2 := pine.NewOperatorInput(map[string]any{"item_count": 0.0}, nil)
	out2 := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in2, out2)

	cw2 := out2.GetCommonWrites()
	if cw2["_if_1"] != true {
		t.Errorf("expected true (skip), got %v", cw2["_if_1"])
	}
}

func TestLuaOpForItemEmpty(t *testing.T) {
	op := newLuaOp(t, `function f() return 1 end`, "f", "",
		nil, nil, nil, []string{"out"})

	in := pine.NewOperatorInput(nil, nil)
	out := pine.NewOperatorOutput()
	err := op.Execute(context.Background(), in, out)
	if err != nil {
		t.Fatal(err)
	}
	// No items — no writes
	if len(out.GetItemWrites()) != 0 {
		t.Error("expected no writes for empty items")
	}
}

func TestLuaOpFunctionNotFound(t *testing.T) {
	op := &LuaOp{}
	params := map[string]any{
		"lua_script":          `function other() return 1 end`,
		"function_for_item":   "missing_func",
		"function_for_common": "",
	}
	err := op.Init(params)
	if err == nil {
		t.Fatal("expected error for missing function")
	}
}

func TestLuaOpNilHandling(t *testing.T) {
	script := `function f()
		if item_val == nil then return -1 else return item_val end
	end`

	op := newLuaOp(t, script, "f", "",
		nil, nil,
		[]string{"item_val"}, []string{"result"})

	// item_val is nil for item 0
	items := []map[string]any{{}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)

	writes := out.ItemWriteMap()
	if writes[0]["result"] != -1.0 {
		t.Errorf("expected -1 for nil input, got %v", writes[0]["result"])
	}
}

func TestLuaOpMultipleReturns(t *testing.T) {
	script := `function f() return item_x * 2, item_x + 10 end`

	op := newLuaOp(t, script, "f", "",
		nil, nil,
		[]string{"item_x"}, []string{"double", "plus10"})

	items := []map[string]any{{"item_x": 5.0}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)

	writes := out.ItemWriteMap()
	if writes[0]["double"] != 10.0 {
		t.Errorf("expected 10, got %v", writes[0]["double"])
	}
	if writes[0]["plus10"] != 15.0 {
		t.Errorf("expected 15, got %v", writes[0]["plus10"])
	}
}

func TestLuaOpConcurrent(t *testing.T) {
	script := `function f() return item_x * 2 end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, []string{"item_x"}, []string{"result"})

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(val float64) {
			defer wg.Done()
			items := []map[string]any{{"item_x": val}}
			in := pine.NewOperatorInput(nil, items)
			out := pine.NewOperatorOutput()
			if err := op.Execute(context.Background(), in, out); err != nil {
				errs <- err
				return
			}
			w := out.ItemWriteMap()
			if w[0]["result"] != val*2 {
				errs <- fmt.Errorf("expected %f, got %v", val*2, w[0]["result"])
			}
		}(float64(i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestLuaOpStringReturn(t *testing.T) {
	script := `function f() return "hello_" .. item_name end`
	op := newLuaOp(t, script, "f", "",
		nil, nil, []string{"item_name"}, []string{"greeting"})

	items := []map[string]any{{"item_name": "world"}}
	in := pine.NewOperatorInput(nil, items)
	out := pine.NewOperatorOutput()
	_ = op.Execute(context.Background(), in, out)

	writes := out.ItemWriteMap()
	if writes[0]["greeting"] != "hello_world" {
		t.Errorf("expected hello_world, got %v", writes[0]["greeting"])
	}
}
