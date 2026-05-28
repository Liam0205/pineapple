package bench

import (
	"context"
	"time"

	pine "github.com/Liam0205/pineapple/pine-go"
)

func init() {
	pine.Register(pine.OperatorSchema{
		Name:        "transform_bench_sleep",
		Type:        pine.OpTypeTransform,
		Description: "Benchmark-only I/O-simulating operator. Sleeps for delay_ms per invocation.",
		Params: map[string]pine.ParamSpec{
			"delay_ms": {Type: "int64", Required: false, Default: int64(5), Description: "Milliseconds to sleep per operator invocation."},
		},
	}, func() pine.Operator {
		return &BenchSleepOp{}
	})
}

type BenchSleepOp struct {
	pine.MetadataHolder
	pine.ConcurrentSafeMarker
	delayMS int
}

func (o *BenchSleepOp) Init(params map[string]any) error {
	if v, ok := params["delay_ms"]; ok {
		switch n := v.(type) {
		case int64:
			o.delayMS = int(n)
		case float64:
			o.delayMS = int(n)
		default:
			o.delayMS = 5
		}
	} else {
		o.delayMS = 5
	}
	return nil
}

func (o *BenchSleepOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	select {
	case <-time.After(time.Duration(o.delayMS) * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	n := in.ItemCount()
	for i := 0; i < n; i++ {
		out.SetItem(i, "_bench_slept", true)
	}
	return nil
}
