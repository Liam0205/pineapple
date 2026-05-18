package benchmarks

import (
	"context"
	"fmt"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators/lua"
)

type isolatedCase struct {
	name       string
	goFactory  func() pine.Operator
	goParams   map[string]any
	luaParams  map[string]any
	itemInput  []string
	itemOutput []string
	itemGen    func(n int) []map[string]any
}

var isolatedCases = []isolatedCase{
	{
		name:       "L1_identity",
		goFactory:  func() pine.Operator { return &benchIdentity{} },
		itemInput:  []string{"item_x"},
		itemOutput: []string{"item_y"},
		luaParams: map[string]any{
			"lua_script":         "function f() return item_x end",
			"function_for_item":  "f",
			"function_for_common": "",
		},
		itemGen: func(n int) []map[string]any {
			items := make([]map[string]any, n)
			for i := range items {
				items[i] = map[string]any{"item_x": float64(i)}
			}
			return items
		},
	},
	{
		name:      "L2_arithmetic",
		goFactory: func() pine.Operator { return &benchArithmetic{} },
		goParams:  map[string]any{"rate": 0.85, "base": 10.0},
		itemInput:  []string{"item_price"},
		itemOutput: []string{"item_result"},
		luaParams: map[string]any{
			"lua_script":         "function f() return item_price * 0.85 + 10.0 end",
			"function_for_item":  "f",
			"function_for_common": "",
		},
		itemGen: func(n int) []map[string]any {
			items := make([]map[string]any, n)
			for i := range items {
				items[i] = map[string]any{"item_price": float64(100 + i)}
			}
			return items
		},
	},
	{
		name:      "L3_branching",
		goFactory: func() pine.Operator { return &benchBranching{} },
		itemInput:  []string{"item_price"},
		itemOutput: []string{"item_discounted"},
		luaParams: map[string]any{
			"lua_script": `function f()
  if item_price > 1000 then return item_price * 0.7
  elseif item_price > 500 then return item_price * 0.8
  elseif item_price > 100 then return item_price * 0.9
  else return item_price * 0.95 end
end`,
			"function_for_item":  "f",
			"function_for_common": "",
		},
		itemGen: func(n int) []map[string]any {
			items := make([]map[string]any, n)
			for i := range items {
				items[i] = map[string]any{"item_price": float64(50 + i*3)}
			}
			return items
		},
	},
	{
		name:      "L4_multi_field",
		goFactory: func() pine.Operator { return &benchMultiField{} },
		goParams:  map[string]any{"w1": 0.5, "w2": 0.3, "w3": 0.2},
		itemInput:  []string{"item_a", "item_b", "item_c"},
		itemOutput: []string{"item_score"},
		luaParams: map[string]any{
			"lua_script": `function f()
  local score = 0.5 * item_a + 0.3 * item_b + 0.2 * item_c
  if score < 0 then score = 0 end
  if score > 1 then score = 1 end
  return score
end`,
			"function_for_item":  "f",
			"function_for_common": "",
		},
		itemGen: func(n int) []map[string]any {
			items := make([]map[string]any, n)
			for i := range items {
				items[i] = map[string]any{
					"item_a": float64(i%10) / 10.0,
					"item_b": float64(i%7) / 7.0,
					"item_c": float64(i%5) / 5.0,
				}
			}
			return items
		},
	},
	{
		name:      "L5_iterative",
		goFactory: func() pine.Operator { return &benchIterative{} },
		itemInput:  []string{"item_x"},
		itemOutput: []string{"item_poly"},
		luaParams: map[string]any{
			"lua_script": `function f()
  local coeffs = {1.0, -0.5, 0.25, -0.125, 0.0625, -0.03125}
  local x = item_x
  local result = coeffs[6]
  for j = 5, 1, -1 do
    result = result * x + coeffs[j]
  end
  return result
end`,
			"function_for_item":  "f",
			"function_for_common": "",
		},
		itemGen: func(n int) []map[string]any {
			items := make([]map[string]any, n)
			for i := range items {
				items[i] = map[string]any{"item_x": float64(i) / float64(n)}
			}
			return items
		},
	},
}

func mustBuildLuaOp(b *testing.B, tc isolatedCase) pine.Operator {
	b.Helper()
	op, _, err := pine.BuildOperator("transform_by_lua", tc.luaParams)
	if err != nil {
		b.Fatal(err)
	}
	if ma, ok := op.(interface {
		SetMetadata([]string, []string, []string, []string)
	}); ok {
		ma.SetMetadata(nil, nil, tc.itemInput, tc.itemOutput)
	}
	return op
}

func mustBuildGoOp(b *testing.B, tc isolatedCase) pine.Operator {
	b.Helper()
	op := tc.goFactory()
	if tc.goParams != nil {
		if err := op.Init(tc.goParams); err != nil {
			b.Fatal(err)
		}
	} else {
		if err := op.Init(nil); err != nil {
			b.Fatal(err)
		}
	}
	if ma, ok := op.(interface {
		SetMetadata([]string, []string, []string, []string)
	}); ok {
		ma.SetMetadata(nil, nil, tc.itemInput, tc.itemOutput)
	}
	return op
}

func BenchmarkIsolated(b *testing.B) {
	ctx := context.Background()
	for _, tc := range isolatedCases {
		for _, n := range []int{100, 1000} {
			items := tc.itemGen(n)
			in := pine.NewOperatorInput(nil, items)

			b.Run(fmt.Sprintf("%s/go_%d", tc.name, n), func(b *testing.B) {
				op := mustBuildGoOp(b, tc)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					out := pine.NewOperatorOutput()
					if err := op.Execute(ctx, in, out); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run(fmt.Sprintf("%s/lua_%d", tc.name, n), func(b *testing.B) {
				op := mustBuildLuaOp(b, tc)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					out := pine.NewOperatorOutput()
					if err := op.Execute(ctx, in, out); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
