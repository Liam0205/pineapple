package benchmarks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	pine "github.com/Liam0205/pineapple"
	_ "github.com/Liam0205/pineapple/operators"
)

// --- helpers ---

func mustBuildEngine(b *testing.B, cfg map[string]any) *pine.Engine {
	b.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		b.Fatal(err)
	}
	engine, err := pine.NewEngine(data)
	if err != nil {
		b.Fatal(err)
	}
	return engine
}

func makeItems(n int) []any {
	items := make([]any, n)
	for i := 0; i < n; i++ {
		items[i] = map[string]any{
			"item_id":       fmt.Sprintf("item_%d", i),
			"item_score":    float64(n - i),
			"item_status":   "online",
			"item_category": fmt.Sprintf("cat_%d", i%5),
		}
	}
	return items
}

func makeConfig(operators map[string]any, pipelineMap map[string]any, contract map[string]any) map[string]any {
	return map[string]any{
		"_PINEAPPLE_VERSION": pine.Version,
		"pipeline_config": map[string]any{
			"operators":    operators,
			"pipeline_map": pipelineMap,
		},
		"pipeline_group": map[string]any{
			"main": map[string]any{"pipeline": []string{"stage1"}},
		},
		"flow_contract": contract,
	}
}

// --- small pipeline: recall -> filter -> sort (3 ops, N items) ---

func smallPipelineConfig(numItems int) map[string]any {
	return makeConfig(
		map[string]any{
			"recall": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     makeItems(numItems),
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
				},
			},
			"filter": map[string]any{
				"type_name": "filter_truncate",
				"top_n":     float64(numItems), // keep all
				"$metadata": map[string]any{
					"item_input":  []string{"item_id"},
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"sort": map[string]any{
				"type_name": "reorder_sort",
				"order":     "desc",
				"$metadata": map[string]any{
					"item_input": []string{"item_score"},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall", "filter", "sort"}}},
		map[string]any{},
	)
}

// --- medium pipeline: recall -> merge -> dispatch -> normalize -> filter -> sort (6 ops) ---

func mediumPipelineConfig(numItems int) map[string]any {
	half := numItems / 2
	return makeConfig(
		map[string]any{
			"recall_a": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     makeItems(half),
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
				},
			},
			"recall_b": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     makeItems(numItems)[half:],
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
				},
			},
			"merge": map[string]any{
				"type_name": "merge_dedup",
				"sources":   []string{"recall_a", "recall_b"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id", "_source"},
					"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
				},
			},
			"dispatch": map[string]any{
				"type_name": "transform_dispatch",
				"$metadata": map[string]any{
					"common_input": []string{"scene"},
					"item_input":   []string{"item_status"},
					"item_output":  []string{"item_scene"},
				},
			},
			"normalize": map[string]any{
				"type_name": "transform_normalize",
				"$metadata": map[string]any{
					"item_input":  []string{"item_score"},
					"item_output": []string{"item_score_norm"},
				},
			},
			"sort": map[string]any{
				"type_name": "reorder_sort",
				"order":     "desc",
				"$metadata": map[string]any{
					"item_input": []string{"item_score", "item_score_norm"},
				},
			},
		},
		map[string]any{
			"stage1": map[string]any{"pipeline": []string{"recall_a", "recall_b", "merge", "dispatch", "normalize", "sort"}},
		},
		map[string]any{"common_input": []string{"scene"}},
	)
}

// --- large pipeline: full 7-op with real filtering ---

func largePipelineConfig(numItems int) map[string]any {
	// Mark 10% as offline
	items := makeItems(numItems)
	for i := 0; i < len(items); i++ {
		if i%10 == 0 {
			items[i].(map[string]any)["item_status"] = "offline"
		}
	}
	half := len(items) / 2
	return makeConfig(
		map[string]any{
			"recall_a": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     items[:half],
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
				},
			},
			"recall_b": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     items[half:],
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
				},
			},
			"merge": map[string]any{
				"type_name": "merge_dedup",
				"sources":   []string{"recall_a", "recall_b"},
				"$metadata": map[string]any{
					"item_input":  []string{"item_id", "_source"},
					"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
				},
			},
			"filter": map[string]any{
				"type_name": "filter_condition",
				"value":     "offline",
				"$metadata": map[string]any{
					"item_input":  []string{"item_status"},
					"item_output": []string{"item_status", "item_score"},
				},
			},
			"dispatch": map[string]any{
				"type_name": "transform_dispatch",
				"$metadata": map[string]any{
					"common_input": []string{"scene"},
					"item_input":   []string{"item_status"},
					"item_output":  []string{"item_scene"},
				},
			},
			"normalize": map[string]any{
				"type_name": "transform_normalize",
				"$metadata": map[string]any{
					"item_input":  []string{"item_score"},
					"item_output": []string{"item_score_norm"},
				},
			},
			"sort": map[string]any{
				"type_name": "reorder_sort",
				"order":     "desc",
				"$metadata": map[string]any{
					"item_input": []string{"item_score", "item_score_norm"},
				},
			},
		},
		map[string]any{
			"stage1": map[string]any{"pipeline": []string{
				"recall_a", "recall_b", "merge", "filter", "dispatch", "normalize", "sort",
			}},
		},
		map[string]any{"common_input": []string{"scene"}},
	)
}

// --- Benchmarks ---

func BenchmarkSmallPipeline_10(b *testing.B) {
	engine := mustBuildEngine(b, smallPipelineConfig(10))
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSmallPipeline_100(b *testing.B) {
	engine := mustBuildEngine(b, smallPipelineConfig(100))
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMediumPipeline_100(b *testing.B) {
	engine := mustBuildEngine(b, mediumPipelineConfig(100))
	req := &pine.Request{Common: map[string]any{"scene": "bench"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMediumPipeline_1000(b *testing.B) {
	engine := mustBuildEngine(b, mediumPipelineConfig(1000))
	req := &pine.Request{Common: map[string]any{"scene": "bench"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLargePipeline_1000(b *testing.B) {
	engine := mustBuildEngine(b, largePipelineConfig(1000))
	req := &pine.Request{Common: map[string]any{"scene": "bench"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLargePipeline_10000(b *testing.B) {
	engine := mustBuildEngine(b, largePipelineConfig(10000))
	req := &pine.Request{Common: map[string]any{"scene": "bench"}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParallelRecall(b *testing.B) {
	// Two independent recalls — measures parallel scheduling benefit
	cfg := makeConfig(
		map[string]any{
			"recall_a": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     makeItems(500),
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"recall_b": map[string]any{
				"type_name": "recall_static",
				"recall":    true,
				"items":     makeItems(500),
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall_a", "recall_b"}}},
		map[string]any{},
	)
	engine := mustBuildEngine(b, cfg)
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkConcurrentExecute_10(b *testing.B) {
	engine := mustBuildEngine(b, mediumPipelineConfig(100))
	req := &pine.Request{Common: map[string]any{"scene": "bench"}}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := engine.Execute(context.Background(), req)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkEngineCreation(b *testing.B) {
	cfg := mediumPipelineConfig(100)
	data, _ := json.Marshal(cfg)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := pine.NewEngine(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// --- allocation benchmarks ---

func BenchmarkSmallPipeline_10_Allocs(b *testing.B) {
	engine := mustBuildEngine(b, smallPipelineConfig(10))
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(context.Background(), req)
	}
}

func BenchmarkLargePipeline_1000_Allocs(b *testing.B) {
	engine := mustBuildEngine(b, largePipelineConfig(1000))
	req := &pine.Request{Common: map[string]any{"scene": "bench"}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(context.Background(), req)
	}
}

// --- Lua benchmarks ---

func luaItemConfig(numItems int) map[string]any {
	items := make([]any, numItems)
	for i := 0; i < numItems; i++ {
		items[i] = map[string]any{
			"item_id":    fmt.Sprintf("item_%d", i),
			"item_price": float64(100 + i),
		}
	}
	return makeConfig(
		map[string]any{
			"recall": map[string]any{
				"type_name": "recall_static",
				"items":     items,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_price"},
				},
			},
			"lua_discount": map[string]any{
				"type_name":          "transform_by_lua",
				"lua_script":         "function f()\n  if user_age < 18 then\n    return item_price * 0.8\n  else\n    return item_price\n  end\nend",
				"function_for_item":  "f",
				"function_for_common": "",
				"$metadata": map[string]any{
					"common_input": []string{"user_age"},
					"item_input":   []string{"item_price"},
					"item_output":  []string{"item_final"},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall", "lua_discount"}}},
		map[string]any{"common_input": []string{"user_age"}},
	)
}

func luaCommonConfig(numItems int) map[string]any {
	items := make([]any, numItems)
	for i := 0; i < numItems; i++ {
		items[i] = map[string]any{
			"item_id":    fmt.Sprintf("item_%d", i),
			"item_price": float64(100 + i),
		}
	}
	return makeConfig(
		map[string]any{
			"recall": map[string]any{
				"type_name": "recall_static",
				"items":     items,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_price"},
				},
			},
			"lua_stats": map[string]any{
				"type_name":          "transform_by_lua",
				"lua_script":         "function f()\n  local sum = 0\n  for i = 1, #item_price do sum = sum + item_price[i] end\n  return sum / #item_price\nend",
				"function_for_item":  "",
				"function_for_common": "f",
				"$metadata": map[string]any{
					"common_output": []string{"avg_price"},
					"item_input":    []string{"item_price"},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall", "lua_stats"}}},
		map[string]any{},
	)
}

func luaControlFlowConfig(numItems int) map[string]any {
	items := make([]any, numItems)
	for i := 0; i < numItems; i++ {
		items[i] = map[string]any{
			"item_id":    fmt.Sprintf("item_%d", i),
			"item_score": float64(numItems - i),
		}
	}
	return makeConfig(
		map[string]any{
			"recall": map[string]any{
				"type_name": "recall_static",
				"items":     items,
				"$metadata": map[string]any{
					"item_output": []string{"item_id", "item_score"},
				},
			},
			"lua_check": map[string]any{
				"type_name":          "transform_by_lua",
				"lua_script":         "function f()\n  if item_count > 100 then return true else return false end\nend",
				"function_for_item":  "",
				"function_for_common": "f",
				"$metadata": map[string]any{
					"common_input":  []string{"item_count"},
					"common_output": []string{"_skip_sort"},
				},
			},
			"sort": map[string]any{
				"type_name": "reorder_sort",
				"order":     "desc",
				"skip":      "_skip_sort",
				"$metadata": map[string]any{
					"common_input": []string{"_skip_sort"},
					"item_input":   []string{"item_score"},
				},
			},
		},
		map[string]any{"stage1": map[string]any{"pipeline": []string{"recall", "lua_check", "sort"}}},
		map[string]any{"common_input": []string{"item_count"}},
	)
}

func BenchmarkLuaItemOp_100(b *testing.B) {
	engine := mustBuildEngine(b, luaItemConfig(100))
	req := &pine.Request{Common: map[string]any{"user_age": 15.0}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLuaItemOp_1000(b *testing.B) {
	engine := mustBuildEngine(b, luaItemConfig(1000))
	req := &pine.Request{Common: map[string]any{"user_age": 15.0}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLuaCommonOp_100(b *testing.B) {
	engine := mustBuildEngine(b, luaCommonConfig(100))
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLuaCommonOp_1000(b *testing.B) {
	engine := mustBuildEngine(b, luaCommonConfig(1000))
	req := &pine.Request{Common: map[string]any{}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLuaControlFlow_100(b *testing.B) {
	engine := mustBuildEngine(b, luaControlFlowConfig(100))
	req := &pine.Request{Common: map[string]any{"item_count": 50.0}}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := engine.Execute(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLuaItemOp_100_Allocs(b *testing.B) {
	engine := mustBuildEngine(b, luaItemConfig(100))
	req := &pine.Request{Common: map[string]any{"user_age": 15.0}}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Execute(context.Background(), req)
	}
}

// --- throughput benchmark (measures parallelism) ---

func BenchmarkThroughput(b *testing.B) {
	engine := mustBuildEngine(b, largePipelineConfig(100))
	req := &pine.Request{Common: map[string]any{"scene": "bench"}}

	for _, parallelism := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("goroutines=%d", parallelism), func(b *testing.B) {
			b.ResetTimer()
			var wg sync.WaitGroup
			per := b.N / parallelism
			if per == 0 {
				per = 1
			}
			for g := 0; g < parallelism; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < per; i++ {
						_, _ = engine.Execute(context.Background(), req)
					}
				}()
			}
			wg.Wait()
		})
	}
}
