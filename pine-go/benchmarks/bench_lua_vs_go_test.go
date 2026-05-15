package benchmarks

import (
	"fmt"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

type luaVsGoCase struct {
	name      string
	luaScript string
	luaFunc   string
	goType    string
	goParams  map[string]any
	itemGen   func(n int) []any
	metadata  map[string]any
	common    map[string]any
}

var luaVsGoCases = []luaVsGoCase{
	{
		name:    "L1_identity",
		luaScript: "function f() return item_x end",
		luaFunc: "f",
		goType:  "bench_identity",
		itemGen: func(n int) []any {
			items := make([]any, n)
			for i := range items {
				items[i] = map[string]any{"item_x": float64(i)}
			}
			return items
		},
		metadata: map[string]any{
			"item_input":  []string{"item_x"},
			"item_output": []string{"item_y"},
		},
	},
	{
		name:      "L2_arithmetic",
		luaScript: "function f() return item_price * 0.85 + 10.0 end",
		luaFunc:   "f",
		goType:    "bench_arithmetic",
		goParams:  map[string]any{"rate": 0.85, "base": 10.0},
		itemGen: func(n int) []any {
			items := make([]any, n)
			for i := range items {
				items[i] = map[string]any{"item_price": float64(100 + i)}
			}
			return items
		},
		metadata: map[string]any{
			"item_input":  []string{"item_price"},
			"item_output": []string{"item_result"},
		},
	},
	{
		name: "L3_branching",
		luaScript: `function f()
  if item_price > 1000 then return item_price * 0.7
  elseif item_price > 500 then return item_price * 0.8
  elseif item_price > 100 then return item_price * 0.9
  else return item_price * 0.95 end
end`,
		luaFunc: "f",
		goType:  "bench_branching",
		itemGen: func(n int) []any {
			items := make([]any, n)
			for i := range items {
				items[i] = map[string]any{"item_price": float64(50 + i*3)}
			}
			return items
		},
		metadata: map[string]any{
			"item_input":  []string{"item_price"},
			"item_output": []string{"item_discounted"},
		},
	},
	{
		name: "L4_multi_field",
		luaScript: `function f()
  local score = 0.5 * item_a + 0.3 * item_b + 0.2 * item_c
  if score < 0 then score = 0 end
  if score > 1 then score = 1 end
  return score
end`,
		luaFunc:  "f",
		goType:   "bench_multi_field",
		goParams: map[string]any{"w1": 0.5, "w2": 0.3, "w3": 0.2},
		itemGen: func(n int) []any {
			items := make([]any, n)
			for i := range items {
				items[i] = map[string]any{
					"item_a": float64(i%10) / 10.0,
					"item_b": float64(i%7) / 7.0,
					"item_c": float64(i%5) / 5.0,
				}
			}
			return items
		},
		metadata: map[string]any{
			"item_input":  []string{"item_a", "item_b", "item_c"},
			"item_output": []string{"item_score"},
		},
	},
	{
		name: "L5_iterative",
		luaScript: `function f()
  local coeffs = {1.0, -0.5, 0.25, -0.125, 0.0625, -0.03125}
  local x = item_x
  local result = coeffs[6]
  for j = 5, 1, -1 do
    result = result * x + coeffs[j]
  end
  return result
end`,
		luaFunc: "f",
		goType:  "bench_iterative",
		itemGen: func(n int) []any {
			items := make([]any, n)
			for i := range items {
				items[i] = map[string]any{"item_x": float64(i) / float64(n)}
			}
			return items
		},
		metadata: map[string]any{
			"item_input":  []string{"item_x"},
			"item_output": []string{"item_poly"},
		},
	},
}

func buildLuaConfig(tc luaVsGoCase, items []any) map[string]any {
	meta := map[string]any{
		"item_output": tc.metadata["item_output"],
	}
	// recall metadata
	recallMeta := map[string]any{
		"item_output": tc.metadata["item_input"],
	}

	luaOp := map[string]any{
		"type_name":          "transform_by_lua",
		"lua_script":         tc.luaScript,
		"function_for_item":  tc.luaFunc,
		"function_for_common": "",
		"$metadata":          meta,
	}
	if inputs, ok := tc.metadata["item_input"]; ok {
		luaOp["$metadata"].(map[string]any)["item_input"] = inputs
	}
	if ci, ok := tc.metadata["common_input"]; ok {
		luaOp["$metadata"].(map[string]any)["common_input"] = ci
	}

	return makeConfig(
		map[string]any{
			"recall": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     items,
				"$metadata": recallMeta,
			},
			"op": luaOp,
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall", "op"}}},
		map[string]any{},
	)
}

func buildGoConfig(tc luaVsGoCase, items []any) map[string]any {
	recallMeta := map[string]any{
		"item_output": tc.metadata["item_input"],
	}

	goOp := map[string]any{
		"type_name": tc.goType,
		"$metadata": copyMap(tc.metadata),
	}
	for k, v := range tc.goParams {
		goOp[k] = v
	}

	return makeConfig(
		map[string]any{
			"recall": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     items,
				"$metadata": recallMeta,
			},
			"op": goOp,
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall", "op"}}},
		map[string]any{},
	)
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func BenchmarkLuaVsGo(b *testing.B) {
	for _, tc := range luaVsGoCases {
		for _, n := range []int{100, 1000} {
			items := tc.itemGen(n)
			common := tc.common
			if common == nil {
				common = map[string]any{}
			}

			b.Run(fmt.Sprintf("%s/lua_%d", tc.name, n), func(b *testing.B) {
				engine := mustBuildEngine(b, buildLuaConfig(tc, items))
				req := &pine.Request{Common: common}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := engine.Execute(b.Context(), req); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run(fmt.Sprintf("%s/go_%d", tc.name, n), func(b *testing.B) {
				engine := mustBuildEngine(b, buildGoConfig(tc, items))
				req := &pine.Request{Common: common}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := engine.Execute(b.Context(), req); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
