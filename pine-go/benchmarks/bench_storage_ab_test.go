package benchmarks

// Row vs column storage_mode A/B benchmarks over the same pipelines.
// TransformHeavy is the shape where column storage is expected to win
// (many items, repeated field scans, no structural changes); Small is a
// recall/filter/sort shape that inherently favors row storage. See
// llmdoc/memory/reflections/column-vs-row-parity-investigation.md.

import (
	"context"
	"fmt"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func benchStorageAB(b *testing.B, mkCfg func(int) map[string]any, numItems int) {
	req := &pine.Request{Common: map[string]any{"scene": "feed"}}
	for _, mode := range []string{"row", "column"} {
		b.Run(mode, func(b *testing.B) {
			cfg := mkCfg(numItems)
			cfg["storage_mode"] = mode
			engine := mustBuildEngine(b, cfg)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := engine.Execute(context.Background(), req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// transformHeavyConfig chains 8 transform_normalize ops over one recall:
// a scan-dominated pipeline with no removals/reorder/additions after the
// initial recall.
func transformHeavyConfig(numItems int) map[string]any {
	ops := map[string]any{
		"recall": map[string]any{
			"type_name": "recall_static",
			"recall":    true,
			"items":     makeItems(numItems),
			"$metadata": map[string]any{
				"item_output": []string{"item_id", "item_score", "item_status", "item_category"},
			},
		},
	}
	pipeline := []string{"recall"}
	prev := "item_score"
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("norm_%d", i)
		outField := fmt.Sprintf("item_score_n%d", i)
		ops[name] = map[string]any{
			"type_name": "transform_normalize",
			"$metadata": map[string]any{
				"item_input":  []string{prev},
				"item_output": []string{outField},
			},
		}
		pipeline = append(pipeline, name)
		prev = outField
	}
	return makeConfig(
		ops,
		map[string]any{"stage1": map[string]any{"pipeline": pipeline}},
		map[string]any{},
	)
}

func BenchmarkStorageAB_Small_1000(b *testing.B) {
	benchStorageAB(b, smallPipelineConfig, 1000)
}

func BenchmarkStorageAB_Large_5000(b *testing.B) {
	benchStorageAB(b, largePipelineConfig, 5000)
}

func BenchmarkStorageAB_TransformHeavy_1000(b *testing.B) {
	benchStorageAB(b, transformHeavyConfig, 1000)
}

func BenchmarkStorageAB_TransformHeavy_5000(b *testing.B) {
	benchStorageAB(b, transformHeavyConfig, 5000)
}
