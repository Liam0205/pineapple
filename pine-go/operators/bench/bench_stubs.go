// Package bench provides benchmark-only stub operators and resource fetchers
// for running realistic production pipeline fixtures without external dependencies.
package bench

import (
	"context"
	"fmt"
	"strconv"

	pine "github.com/Liam0205/pineapple/pine-go"
	"github.com/Liam0205/pineapple/pine-go/pkg/resource"
)

func init() {
	// ─── Stub operators ──────────────────────────────────────────────────

	pine.Register(pine.OperatorSchema{
		Name: "recall_feed_data", Type: pine.OpTypeRecall,
		Description: "Benchmark stub: generates synthetic feed items.",
		Params: map[string]pine.ParamSpec{
			"bench_item_count": {Type: "int64", Required: false, Default: int64(3000), Description: "Number of items to generate."},
			"resource_name":    {Type: "string", Required: false, Default: "", Description: "Ignored in stub."},
		},
	}, func() pine.Operator { return &recallFeedDataStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_redis_zrangebyscore", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: simulates Redis ZRANGEBYSCORE.",
		Params: map[string]pine.ParamSpec{
			"key_prefix":     {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"window_seconds": {Type: "int64", Required: false, Default: int64(0), Description: "Stub param."},
			"redis_addr":     {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"redis_password": {Type: "string", Required: false, Default: "", Description: "Stub param."},
		},
	}, func() pine.Operator { return &transformRedisZrangebyscoreStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_hydrate", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: simulates MySQL hydration.",
		Params: map[string]pine.ParamSpec{
			"mysql_dsn": {Type: "string", Required: false, Default: "", Description: "Stub param."},
		},
	}, func() pine.Operator { return &transformHydrateStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_query_blocked_creators", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: simulates MySQL blocked-creators query.",
		Params: map[string]pine.ParamSpec{
			"mysql_dsn": {Type: "string", Required: false, Default: "", Description: "Stub param."},
		},
	}, func() pine.Operator { return &transformQueryBlockedCreatorsStub{} })
// PLACEHOLDER_MORE_OPS

	pine.Register(pine.OperatorSchema{
		Name: "filter_impression", Type: pine.OpTypeFilter,
		Description: "Benchmark stub: simulates impression-based filtering.",
		Params: map[string]pine.ParamSpec{
			"min_remaining_ratio": {Type: "float64", Required: false, Default: 1.5, Description: "Stub param."},
		},
	}, func() pine.Operator { return &filterImpressionStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "filter_blocked_creator", Type: pine.OpTypeFilter,
		Description: "Benchmark stub: simulates blocked-creator filtering.",
	}, func() pine.Operator { return &filterBlockedCreatorStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "reorder_topn_boost", Type: pine.OpTypeReorder,
		Description: "Benchmark stub: simulates top-N boost reordering.",
		Params: map[string]pine.ParamSpec{
			"size": {Type: "int64", Required: false, Default: int64(10), Description: "Stub param."},
		},
	}, func() pine.Operator { return &reorderTopnBoostStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "observe_datahub", Type: pine.OpTypeObserve,
		Description: "Benchmark stub: simulates DataHub MQ write.",
		Params: map[string]pine.ParamSpec{
			"resource_name": {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"mode":          {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"key_fields":    {Type: "[]string", Required: false, Default: nil, Description: "Stub param."},
		},
	}, func() pine.Operator { return &observeDatahubStub{} })

	pine.Register(pine.OperatorSchema{
		Name: "transform_generate_request_id", Type: pine.OpTypeTransform,
		Description: "Benchmark stub: generates a fixed request ID.",
		Params: map[string]pine.ParamSpec{
			"prefix": {Type: "string", Required: false, Default: "bench", Description: "Stub param."},
		},
	}, func() pine.Operator { return &transformGenerateRequestIdStub{} })

	// ─── Stub resource fetchers ──────────────────────────────────────────

	pine.RegisterResource(pine.ResourceSchema{
		Name: "feed_data", Description: "Benchmark stub: generates synthetic feed data.",
		Params: map[string]pine.ParamSpec{
			"mysql_dsn": {Type: "string", Required: false, Default: "", Description: "Stub param."},
		},
	}, func(params map[string]any) (resource.Fetcher, error) {
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
			"max_retry":  {Type: "int64", Required: false, Default: int64(0), Description: "Stub param."},
			"project":    {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"topic":      {Type: "string", Required: false, Default: "", Description: "Stub param."},
			"user_agent": {Type: "string", Required: false, Default: "", Description: "Stub param."},
		},
	}, func(params map[string]any) (resource.Fetcher, error) {
		return func(ctx context.Context) (any, error) { return "nop", nil }, nil
	})
}

// ─── Operator implementations ────────────────────────────────────────────────

type recallFeedDataStub struct {
	pine.MetadataHolder
	pine.AdditiveWritesRowSetMarker
	itemCount int
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
	return nil
}

type transformRedisZrangebyscoreStub struct{ pine.MetadataHolder }

func (o *transformRedisZrangebyscoreStub) Init(_ map[string]any) error { return nil }
func (o *transformRedisZrangebyscoreStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	_ = in.Common("user_id")
	out.SetCommon("impression_ids", []any{})
	out.SetCommon("impression_cache_hit", true)
	out.SetCommon("impression_ids_len", float64(0))
	return nil
}

type transformHydrateStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
}

func (o *transformHydrateStub) Init(_ map[string]any) error { return nil }
func (o *transformHydrateStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	for i := 0; i < in.ItemCount(); i++ {
		_ = in.Item(i, "item_id")
		_ = in.Item(i, "type")
		out.SetItem(i, "creator_id", float64(i%1000))
	}
	return nil
}

type transformQueryBlockedCreatorsStub struct{ pine.MetadataHolder }

func (o *transformQueryBlockedCreatorsStub) Init(_ map[string]any) error { return nil }
func (o *transformQueryBlockedCreatorsStub) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
	_ = in.Common("user_id")
	_ = in.Common("blocked_creator_ids")
	out.SetCommon("blocked_creator_ids", []any{})
	return nil
}

type filterImpressionStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
}

func (o *filterImpressionStub) Init(_ map[string]any) error { return nil }
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
	return nil
}

type filterBlockedCreatorStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
}

func (o *filterBlockedCreatorStub) Init(_ map[string]any) error { return nil }
func (o *filterBlockedCreatorStub) Execute(_ context.Context, in *pine.OperatorInput, _ *pine.OperatorOutput) error {
	_ = in.Common("blocked_creator_ids")
	for i := 0; i < in.ItemCount(); i++ {
		_ = in.Item(i, "creator_id")
	}
	return nil
}

type reorderTopnBoostStub struct {
	pine.MetadataHolder
	pine.ConsumesRowSetMarker
	pine.MutatesRowSetMarker
}

func (o *reorderTopnBoostStub) Init(_ map[string]any) error { return nil }
func (o *reorderTopnBoostStub) Execute(_ context.Context, in *pine.OperatorInput, _ *pine.OperatorOutput) error {
	_ = in.Common("page")
	_ = in.Common("shuffle_salt")
	for i := 0; i < in.ItemCount(); i++ {
		_ = in.Item(i, "id")
		_ = in.Item(i, "created_at")
	}
	return nil
}

type observeDatahubStub struct{ pine.MetadataHolder }

func (o *observeDatahubStub) Init(_ map[string]any) error { return nil }
func (o *observeDatahubStub) Execute(_ context.Context, in *pine.OperatorInput, _ *pine.OperatorOutput) error {
	for _, k := range o.CommonInput {
		_ = in.Common(k)
	}
	for i := 0; i < in.ItemCount(); i++ {
		for _, k := range o.ItemInput {
			_ = in.Item(i, k)
		}
	}
	return nil
}

type transformGenerateRequestIdStub struct {
	pine.MetadataHolder
	prefix string
}

func (o *transformGenerateRequestIdStub) Init(params map[string]any) error {
	o.prefix = "bench"
	if v, ok := params["prefix"]; ok {
		if s, ok := v.(string); ok {
			o.prefix = s
		}
	}
	return nil
}

func (o *transformGenerateRequestIdStub) Execute(_ context.Context, _ *pine.OperatorInput, out *pine.OperatorOutput) error {
	out.SetCommon("request_id", fmt.Sprintf("%s:550e8400-e29b-41d4-a716-446655440000", o.prefix))
	return nil
}
