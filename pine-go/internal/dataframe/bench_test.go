package dataframe

import (
	"fmt"
	"testing"

	"github.com/Liam0205/pineapple/pine-go/internal/types"
)

const (
	benchItems  = 1000
	benchFields = 10
)

func makeBenchItems(n, fields int) (map[string]any, []map[string]any) {
	common := map[string]any{"user_id": "u_bench", "age": int64(30)}
	items := make([]map[string]any, n)
	for i := 0; i < n; i++ {
		row := make(map[string]any, fields)
		for f := 0; f < fields; f++ {
			row[fmt.Sprintf("field_%d", f)] = float64(i*fields + f)
		}
		items[i] = row
	}
	return common, items
}

func benchFieldNames(n int) []string {
	names := make([]string, n)
	for i := 0; i < n; i++ {
		names[i] = fmt.Sprintf("field_%d", i)
	}
	return names
}

func BenchmarkNew(b *testing.B) {
	common, items := makeBenchItems(benchItems, benchFields)
	for _, tm := range testModes {
		b.Run(tm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = NewFrame(tm.mode, common, items)
			}
		})
	}
}

func BenchmarkBuildInput(b *testing.B) {
	common, items := makeBenchItems(benchItems, benchFields)
	fields := benchFieldNames(5)
	for _, tm := range testModes {
		b.Run(tm.name, func(b *testing.B) {
			f := NewFrame(tm.mode, common, items)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = f.BuildInput([]string{"user_id", "age"}, fields, nil, nil)
			}
		})
	}
}

func BenchmarkApplyOutput_ItemWrites(b *testing.B) {
	common, items := makeBenchItems(benchItems, benchFields)
	for _, tm := range testModes {
		b.Run(tm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				f := NewFrame(tm.mode, common, items)
				out := types.NewOperatorOutput()
				for j := 0; j < benchItems; j++ {
					out.SetItem(j, "field_0", float64(j)*2)
					out.SetItem(j, "field_1", float64(j)*3)
					out.SetItem(j, "field_2", float64(j)*4)
				}
				b.StartTimer()
				_ = f.ApplyOutput(out, "bench_op", false)
			}
		})
	}
}

func BenchmarkApplyOutput_Removals(b *testing.B) {
	common, items := makeBenchItems(benchItems, benchFields)
	for _, tm := range testModes {
		b.Run(tm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				f := NewFrame(tm.mode, common, items)
				out := types.NewOperatorOutput()
				for j := 0; j < benchItems; j += 2 {
					out.RemoveItem(j)
				}
				b.StartTimer()
				_ = f.ApplyOutput(out, "bench_op", false)
			}
		})
	}
}

func BenchmarkApplyOutput_Reorder(b *testing.B) {
	common, items := makeBenchItems(benchItems, benchFields)
	order := make([]int, benchItems)
	for i := 0; i < benchItems; i++ {
		order[i] = benchItems - 1 - i
	}
	for _, tm := range testModes {
		b.Run(tm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				f := NewFrame(tm.mode, common, items)
				out := types.NewOperatorOutput()
				out.SetItemOrder(order)
				b.StartTimer()
				_ = f.ApplyOutput(out, "bench_op", false)
			}
		})
	}
}

func BenchmarkApplyOutput_Additions(b *testing.B) {
	addedItems := make([]map[string]any, benchItems)
	for i := 0; i < benchItems; i++ {
		row := make(map[string]any, benchFields)
		for f := 0; f < benchFields; f++ {
			row[fmt.Sprintf("field_%d", f)] = float64(i*benchFields + f)
		}
		addedItems[i] = row
	}
	for _, tm := range testModes {
		b.Run(tm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				f := NewFrame(tm.mode, nil, nil)
				out := types.NewOperatorOutput()
				for _, item := range addedItems {
					out.AddItem(item)
				}
				b.StartTimer()
				_ = f.ApplyOutput(out, "recall_bench", true)
			}
		})
	}
}

func BenchmarkToResult(b *testing.B) {
	common, items := makeBenchItems(benchItems, benchFields)
	fields := benchFieldNames(5)
	for _, tm := range testModes {
		b.Run(tm.name, func(b *testing.B) {
			f := NewFrame(tm.mode, common, items)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = f.ToResult([]string{"user_id"}, fields)
			}
		})
	}
}
