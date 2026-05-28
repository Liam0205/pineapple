package bench

import (
	"context"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_bench_cpu",
		Type:        pine.OpTypeTransform,
		Description: "Benchmark-only CPU-bound operator. Computes iterative fib per item. Not available in pine-python.",
		Params: map[string]pine.ParamSpec{
			"iterations": {Type: "int64", Required: false, Default: int64(100), Description: "Number of fib(32) computations per item."},
		},
	}, func() pine.Operator {
		return &BenchCPUOp{}
	})
}

type BenchCPUOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	iterations int
}

func (o *BenchCPUOp) Init(params map[string]any) error {
	if v, ok := params["iterations"]; ok {
		switch n := v.(type) {
		case int64:
			o.iterations = int(n)
		case float64:
			o.iterations = int(n)
		default:
			o.iterations = 100
		}
	} else {
		o.iterations = 100
	}
	return nil
}

func (o *BenchCPUOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	n := in.ItemCount()
	for i := 0; i < n; i++ {
		result := cpuWork(o.iterations)
		out.SetItem(i, "_bench_result", result)
	}
	return nil
}

func cpuWork(iterations int) float64 {
	var acc float64
	for i := 0; i < iterations; i++ {
		acc += float64(fib(32))
		acc /= 1.000001
	}
	return acc
}

func fib(n int) int64 {
	if n <= 1 {
		return int64(n)
	}
	var a, b int64 = 0, 1
	for i := 2; i <= n; i++ {
		a, b = b, a+b
	}
	return b
}
