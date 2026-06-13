//go:build pine_bench

// Calibrated end-to-end benchmark: loads the realistic_for_you_calibrated
// production-proxy fixture from disk and drives the full engine (recall →
// 17 transform_by_lua control-flow ops → filter/reorder/observe) per request.
//
// Purpose: this is the *only* judge for the wangshu-vs-gopher-lua backend
// decision (see llmdoc/guides/benchmark-hygiene.md — calibrated fixtures are
// the sole arbiter of perf decisions). wangshu is the default backend; gopher-lua
// is opt-in via lua_gopher. Run the same target under both and diff with benchstat:
//
//	go test -tags='pine_bench lua_gopher' -run='^$' -bench=BenchmarkCalibrated -count=10 ./... > gopher.txt
//	go test -tags='pine_bench'            -run='^$' -bench=BenchmarkCalibrated -count=10 ./... > wangshu.txt
//	benchstat gopher.txt wangshu.txt
//
// Requires the pine_bench build tag so the external-dependency operators
// (recall_feed_data / redis / mysql hydrate / datahub) resolve to throughput
// stubs in operators/bench/bench_stubs.go.
package benchmarks

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"testing"

	pine "github.com/Liam0205/pineapple/pine-go"
	_ "github.com/Liam0205/pineapple/pine-go/operators"
)

// calibratedFixtures are the production-proxy configs benchmarked under both
// Lua backends. The base variant generates 3000 stub feed items; the 2c4g
// variant mirrors the constrained-deployment shape used by bench-profile.sh.
// The itemlua variant adds a per-item transform_by_lua scoring op (3000 per-item
// VM boundary crossings/request) — the boundary-dominated shape where wangshu's
// CallInto zero-alloc path (issue #8) is expected to pull ahead, unlike the base
// variant whose 17 Lua ops are all common-mode single-shot.
var calibratedFixtures = []string{
	"realistic_for_you_calibrated",
	"realistic_for_you_calibrated_2c4g",
	"realistic_for_you_calibrated_itemlua",
}

func loadCalibrated(b *testing.B, name string) (*pine.Engine, *pine.Request) {
	b.Helper()
	cfg, err := os.ReadFile("../../fixtures/benchmarks/" + name + "_config.json")
	if err != nil {
		b.Fatal(err)
	}
	reqRaw, err := os.ReadFile("../../fixtures/benchmarks/" + name + "_request.json")
	if err != nil {
		b.Fatal(err)
	}
	eng, err := pine.NewEngine(cfg)
	if err != nil {
		b.Fatalf("NewEngine(%s): %v", name, err)
	}
	var req pine.Request
	if err := json.Unmarshal(reqRaw, &req); err != nil {
		b.Fatalf("unmarshal request(%s): %v", name, err)
	}
	if req.Common == nil {
		req.Common = map[string]any{}
	}
	return eng, &req
}

// BenchmarkCalibrated drives each calibrated fixture end-to-end. The fixture's
// 17 transform_by_lua ops are all common-mode control-flow predicates (single
// if/return lines, the dominant production Lua shape), so this measures the
// boundary-cost-dominated regime that the synthetic L1-L3 cases model.
func BenchmarkCalibrated(b *testing.B) {
	// Silence the fixture's observe_log operators so per-request stderr writes
	// don't dominate the measurement or flood the bench output.
	prevOut := log.Writer()
	log.SetOutput(io.Discard)
	defer log.SetOutput(prevOut)

	ctx := context.Background()
	for _, name := range calibratedFixtures {
		eng, req := loadCalibrated(b, name)
		// Warm the Lua state pools so steady-state borrow/reuse — not first-touch
		// state construction — is what we measure.
		if _, err := eng.Execute(ctx, req); err != nil {
			b.Fatalf("warmup(%s): %v", name, err)
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := eng.Execute(ctx, req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
