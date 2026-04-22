package benchmarks

import (
	"context"
	"fmt"
	"testing"

	pine "github.com/Liam0205/pineapple"
	_ "github.com/Liam0205/pineapple/operators"
)

func BenchmarkDataParallel(b *testing.B) {
	for _, nItems := range []int{100, 1000, 10000} {
		items := make([]map[string]any, nItems)
		for i := range items {
			items[i] = map[string]any{"item_x": float64(i) / float64(nItems)}
		}

		for _, shards := range []int{1, 2, 4, 8} {
			opCfg := map[string]any{
				"type_name": "bench_iterative",
				"$metadata": map[string]any{
					"common_input":  []string{},
					"common_output": []string{},
					"item_input":    []string{"item_x"},
					"item_output":   []string{"item_poly"},
				},
			}
			if shards > 1 {
				opCfg["data_parallel"] = shards
			}

			cfg := makeConfig(
				map[string]any{"op": opCfg},
				map[string]any{"stage1": map[string]any{"pipeline": []string{"op"}}},
				map[string]any{
					"common_input": []string{},
					"item_input":   []string{"item_x"},
					"item_output":  []string{"item_poly"},
				},
			)

			engine := mustBuildEngine(b, cfg)
			req := &pine.Request{
				Common: map[string]any{},
				Items:  items,
			}

			b.Run(fmt.Sprintf("items=%d/shards=%d", nItems, shards), func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, err := engine.Execute(context.Background(), req)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
