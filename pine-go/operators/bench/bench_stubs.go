//go:build pine_bench

// Package bench provides benchmark-only stub operators and resource fetchers
// for running realistic production pipeline fixtures without external dependencies.
//
// Diff-fuzz exclusion (audit L3): every operator registered here is gated
// behind the pine_bench build tag and is intentionally absent from
// scripts/differential-fuzz.py op_types. The stubs are throughput-only and
// have no cases/expected fixture surface; including them would force the
// cross-runtime oracle to reason about timing-derived outputs that are not
// part of the engine's pure-function contract. byte-equal work parity for
// transform_bench_cpu is locked separately by
// fixtures/pipelines/bench_cpu_work_parity.json (audit M12).
package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"runtime"
	"sort"
	"strconv"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/metrics"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func init() {
	// ─── Stub operators ──────────────────────────────────────────────────

	pine.Register(pine.OperatorSchema{
		Name: "recall_feed_data", Type: pine.OpTypeRecall,
		Description: "Benchmark stub: generates synthetic feed items.",
		Params: map[string]pine.ParamSpec{
			"bench_item_count": {Type: "int", Required: false, Default: int(3000), Description: "Number of items to generate."},
			"resource_name":    {Type: "string", Required: false, Default: "", Description: "Ignored in stub."},
			"bench_profile":    {Type: "any", Required: false, Default: nil, Description: "Latency profile: {p50:[mean,max], p99:[mean,max], type:cpu|io}."},
		},
	}, func() pine.Operator { return &recallFeedDataStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_redis_zrangebyscore", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: simulates Redis ZRANGEBYSCORE.",
		Params: map[string]pine.ParamSpec{
			"resource_name":  {Type: "string", Required: false, Default: "", Description: "Name of a redis_connection resource (ignored in stub)."},
			"key_prefix":     {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"window_seconds": {Type: "int", Required: false, Default: int(0), Description: "Stub param."},
			"redis_addr":     {Type: "string", Required: false, Default: "", Description: "Legacy stub param."},
			"redis_password": {Type: "string", Required: false, Default: "", Description: "Legacy stub param."},
			"bench_profile":  {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &transformRedisZrangebyscoreStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_hydrate", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: simulates MySQL hydration.",
		Params: map[string]pine.ParamSpec{
			"mysql_dsn":     {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"bench_profile": {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &transformHydrateStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_query_blocked_creators", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: simulates MySQL blocked-creators query.",
		Params: map[string]pine.ParamSpec{
			"mysql_dsn":     {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"bench_profile": {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &transformQueryBlockedCreatorsStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "filter_impression", Type: pine.OpTypeFilter,
		Description: "Benchmark stub: simulates impression-based filtering.",
		Params: map[string]pine.ParamSpec{
			"min_remaining_ratio": {Type: "float", Required: false, Default: 1.5, Description: "Stub param."},
			"bench_profile":       {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &filterImpressionStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "filter_blocked_creator", Type: pine.OpTypeFilter,
		Description: "Benchmark stub: simulates blocked-creator filtering.",
		Params: map[string]pine.ParamSpec{
			"bench_profile": {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &filterBlockedCreatorStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "reorder_topn_boost", Type: pine.OpTypeReorder,
		Description: "Benchmark stub: simulates top-N boost reordering.",
		Params: map[string]pine.ParamSpec{
			"size":          {Type: "int", Required: false, Default: int(10), Description: "Stub param."},
			"bench_profile": {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &reorderTopnBoostStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "observe_datahub", Type: pine.OpTypeObserve,
		Description: "Benchmark stub: simulates DataHub MQ write.",
		Params: map[string]pine.ParamSpec{
			"resource_name": {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"mode":          {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"key_fields":    {Type: "array", Required: false, Default: nil, Description: "Stub param."},
			"bench_profile": {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &observeDatahubStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_generate_request_id", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: generates a fixed request ID.",
		Params: map[string]pine.ParamSpec{
			"prefix":        {Type: "string", Required: false, Default: "bench", Description: "Stub param."},
			"bench_profile": {Type: "any", Required: false, Default: nil, Description: "Latency profile."},
		},
	}, func() pine.Operator { return &transformGenerateRequestIdStub{} })

	// ─── Stub resource fetchers ──────────────────────────────────────────

	pine.RegisterResource(pine.ResourceSchema{
		Name: "feed_data", Description: "Benchmark stub: generates synthetic feed data.",
		Params: map[string]pine.ParamSpec{
			"mysql_dsn": {Type: "string", Required: false, Default: "", Description: "Stub param."},
		},
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) {
			items := make([]map[string]any, 3000)
			for i := range items {
				items[i] = map[string]any{
					"id":         float64(i + 1),
					"item_id":    strconv.Itoa(10000 + i),
					"type":       float64(i%3 + 1),
					"score":      float64(1000 - i),
					"created_at": "2026-01-01T00:00:00Z",
				}
			}
			return items, nil
		}, nil
	})

	pine.RegisterResource(pine.ResourceSchema{
		Name: "datahub_producer", Description: "Benchmark stub: no-op datahub producer.",
		Params: map[string]pine.ParamSpec{
			"ak_id":      {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"ak_secret":  {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"endpoint":   {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"max_retry":  {Type: "int", Required: false, Default: int(0), Description: "Stub param."},
			"project":    {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"topic":      {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"user_agent": {Type: "string", Required: false, Default: "", Description: "Stub param."},
		},
	}, func(params map[string]any, _ metrics.Provider) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) { return "nop", nil }, nil
	})
}

// ─── Operator implementations ────────────────────────────────────────────────

type recallFeedDataStub struct {
	pine.MetadataHolder
	pine.AdditiveWritesRowSetMarker
	itemCount int
	latency   *LatencySampler
}

func (o *recallFeedDataStub) Init(params map[string]any) error {
	o.itemCount = 3000
	if v, ok := params["bench_item_count"]; ok {
		switch n := v.(type) {
		case float64:
			o.itemCount = int(n)
		case int64:
			o.itemCount = int(n)
		}
	}
	o.latency = ParseBenchProfile(params)
	return nil
}

func (o *recallFeedDataStub) Execute(_ context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	for i := 0; i < o.itemCount; i++ {
		out.AddItem(map[string]any{
			"id":         float64(i + 1),
			"item_id":    strconv.Itoa(10000 + i),
			"type":       float64(i%3 + 1),
			"score":      float64(1000 - i),
			"created_at": "2026-01-01T00:00:00Z",
		})
	}
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type transformRedisZrangebyscoreStub struct {
	pine.MetadataHolder
	latency *LatencySampler
}

func (o *transformRedisZrangebyscoreStub) Init(params map[string]any) error {
	o.latency = ParseBenchProfile(params)
	return nil
}
func (o *transformRedisZrangebyscoreStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	_ = in.Common("user_id")
	out.SetCommon("impression_ids", []any{})
	out.SetCommon("impression_cache_hit", true)
	out.SetCommon("impression_ids_len", float64(0))
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type transformHydrateStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	latency *LatencySampler
}

func (o *transformHydrateStub) Init(params map[string]any) error {
	o.latency = ParseBenchProfile(params)
	return nil
}
func (o *transformHydrateStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		_ = in.Item(i, "item_id")
		_ = in.Item(i, "type")
		out.SetItem(i, "creator_id", float64(i%1000))
	}
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type transformQueryBlockedCreatorsStub struct {
	pine.MetadataHolder
	latency *LatencySampler
}

func (o *transformQueryBlockedCreatorsStub) Init(params map[string]any) error {
	o.latency = ParseBenchProfile(params)
	return nil
}
func (o *transformQueryBlockedCreatorsStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	_ = in.Common("user_id")
	_ = in.Common("blocked_creator_ids")
	out.SetCommon("blocked_creator_ids", []any{})
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type filterImpressionStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	latency *LatencySampler
}

func (o *filterImpressionStub) Init(params map[string]any) error {
	o.latency = ParseBenchProfile(params)
	return nil
}
func (o *filterImpressionStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	_ = in.Common("impression_ids")
	_ = in.Common("size")
	for i := 0; i < in.ItemCount(); i++ {
		_ = in.Item(i, "item_id")
		_ = in.Item(i, "type")
		if i%5 == 0 {
			out.RemoveItem(i)
		}
	}
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type filterBlockedCreatorStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	latency *LatencySampler
}

func (o *filterBlockedCreatorStub) Init(params map[string]any) error {
	o.latency = ParseBenchProfile(params)
	return nil
}
func (o *filterBlockedCreatorStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	_ = in.Common("blocked_creator_ids")
	for i := 0; i < in.ItemCount(); i++ {
		_ = in.Item(i, "creator_id")
	}
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type reorderTopnBoostStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
	size    int
	latency *LatencySampler
}

func (o *reorderTopnBoostStub) Init(params map[string]any) error {
	o.size = 10
	if v, ok := params["size"]; ok {
		switch n := v.(type) {
		case int:
			o.size = n
		case int64:
			o.size = int(n)
		case float64:
			o.size = int(n)
		}
	}
	o.latency = ParseBenchProfile(params)
	return nil
}

// Execute performs a deterministic top-N boost: items are ranked by an FNV-1a
// hash of "shuffle_salt | id" (mirroring reorder_shuffle_by_salt), then the
// top `size` items by hash are boosted to the front. The remaining items keep
// their original relative order. This exercises the row-set reorder path
// (SetItemOrder) under load, which a field-only stub would not.
func (o *reorderTopnBoostStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	n := in.ItemCount()
	if n > 0 {
		saltPrefix := benchAnyToString(in.Common("shuffle_salt")) + "|"

		type ranked struct {
			idx int
			r   float64
			id  uint64
		}
		items := make([]ranked, n)
		for i := 0; i < n; i++ {
			itemVal := benchAnyToString(in.Item(i, "id"))
			items[i] = ranked{idx: i, r: benchHashToUnitInterval(saltPrefix + itemVal), id: benchParseUint64(itemVal)}
		}

		boost := o.size
		if boost > n {
			boost = n
		}
		if boost < 0 {
			boost = 0
		}

		// Partial selection: top `boost` items by hash (ascending), tie-broken
		// by parsed id then original index for determinism.
		less := func(a, b int) bool {
			if items[a].r != items[b].r {
				return items[a].r < items[b].r
			}
			if items[a].id != items[b].id {
				return items[a].id < items[b].id
			}
			return items[a].idx < items[b].idx
		}
		sort.Slice(items, less)

		boosted := make([]bool, n)
		order := make([]int, 0, n)
		for i := 0; i < boost; i++ {
			order = append(order, items[i].idx)
			boosted[items[i].idx] = true
		}
		// Remaining items keep their original relative order.
		for i := 0; i < n; i++ {
			if !boosted[i] {
				order = append(order, i)
			}
		}
		out.SetItemOrder(order)
	}
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type observeDatahubStub struct {
	pine.MetadataHolder
	latency *LatencySampler
}

func (o *observeDatahubStub) Init(params map[string]any) error {
	o.latency = ParseBenchProfile(params)
	return nil
}
func (o *observeDatahubStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	for _, k := range o.CommonInput {
		_ = in.Common(k)
	}
	for i := 0; i < in.ItemCount(); i++ {
		for _, k := range o.ItemInput {
			_ = in.Item(i, k)
		}
	}
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

type transformGenerateRequestIdStub struct {
	pine.MetadataHolder
	prefix  string
	latency *LatencySampler
}

func (o *transformGenerateRequestIdStub) Init(params map[string]any) error {
	o.prefix = "bench"
	if v, ok := params["prefix"]; ok {
		if s, ok := v.(string); ok {
			o.prefix = s
		}
	}
	o.latency = ParseBenchProfile(params)
	return nil
}

func (o *transformGenerateRequestIdStub) Execute(_ context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	out.SetCommon("request_id", fmt.Sprintf("%s:550e8400-e29b-41d4-a716-446655440000", o.prefix))
	if o.latency != nil {
		sink := o.latency.Apply()
		runtime.KeepAlive(sink)
	}
	return nil
}

// ─── Deterministic hash helpers (mirror reorder_shuffle_by_salt) ─────────────

func benchHashToUnitInterval(s string) float64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return float64(h.Sum64()) / (float64(math.MaxUint64) + 1.0)
}

func benchAnyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case int64:
		return fmt.Sprintf("%g", float64(x))
	case uint64:
		return fmt.Sprintf("%g", float64(x))
	case float64:
		return fmt.Sprintf("%g", x)
	case int:
		return fmt.Sprintf("%g", float64(x))
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

func benchParseUint64(s string) uint64 {
	n, _ := strconv.ParseUint(s, 10, 64)
	return n
}
