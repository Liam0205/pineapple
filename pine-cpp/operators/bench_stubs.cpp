#include "pine/operator.hpp"
#include "pine/resource.hpp"

#include <algorithm>
#include <charconv>
#include <cstdint>
#include <random>
#include <string>
#include <thread>
#include <vector>

#include "operators/_helpers.hpp"
#include "operators/bench_latency.hpp"

namespace pine {

// ─── Stub resource fetchers ──────────────────────────────────────────────────
// Register fetcher factories for resource types used in realistic benchmarks
// but not implemented in pine-cpp (they live in the business application).

namespace {

const bool _bench_fetcher_feed_data =
    resource::register_fetcher_factory("feed_data", [](const Variant& /*params*/,
                                                       metrics::Provider* /*mp*/) -> resource::Fetcher {
      return []() -> Variant {
        Variant::array_t items;
        items.reserve(3000);
        for (int i = 0; i < 3000; ++i) {
          Variant::object_t row;
          row["id"] = Variant(static_cast<double>(i + 1));
          row["item_id"] = Variant(std::to_string(10000 + i));
          row["type"] = Variant(static_cast<double>(i % 3 + 1));
          row["score"] = Variant(static_cast<double>(1000 - i));
          row["created_at"] = Variant("2026-01-01T00:00:00Z");
          items.push_back(Variant(std::move(row)));
        }
        return Variant(std::move(items));
      };
    });

const bool _bench_fetcher_datahub = resource::register_fetcher_factory(
    "datahub_producer", [](const Variant& /*params*/, metrics::Provider* /*mp*/) -> resource::Fetcher {
      return []() -> Variant { return Variant(nullptr); };
    });

// Deterministic hash helpers (mirror reorder_shuffle_by_salt) for the
// reorder_topn_boost stub, so the bench reorder path matches across runtimes.
uint64_t bench_fnv64a(const std::string& s) {
  uint64_t hash = 14695981039346656037ULL;
  for (unsigned char c : s) {
    hash ^= c;
    hash *= 1099511628211ULL;
  }
  return hash;
}

uint64_t bench_parse_uint64(const std::string& s) {
  uint64_t result = 0;
  std::from_chars(s.data(), s.data() + s.size(), result);
  return result;
}

}  // namespace

// ─── recall_feed_data ────────────────────────────────────────────────────────
// Stub: generates N synthetic items (default 3000) with fields matching the
// real operator's $metadata.item_output: id, item_id, type, score, created_at.

class RecallFeedDataStubOp : public Operator, public AdditiveWritesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    const auto& params = cfg.params.as_object();
    auto it = params.find("bench_item_count");
    if (it != params.end() && it->second.is_number()) {
      item_count_ = static_cast<int>(it->second.as_number());
    }
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& /*input*/, OperatorOutput& out) override {
    for (int i = 0; i < item_count_; ++i) {
      Variant::object_t row;
      row["id"] = Variant(static_cast<double>(i + 1));
      row["item_id"] = Variant(std::to_string(10000 + i));
      row["type"] = Variant(static_cast<double>(i % 3 + 1));
      row["score"] = Variant(static_cast<double>(1000 - i));
      row["created_at"] = Variant("2026-01-01T00:00:00Z");
      out.add_item(std::move(row));
    }
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  int item_count_ = 3000;
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_recall_feed_data_schema{
    .name = "recall_feed_data",
    .type = OpType::Recall,
    .description = "Benchmark stub: generates synthetic feed items.",
    .params = {{"bench_item_count",
                {.type = "int",
                 .required = false,
                 .default_value = Variant(3000.0),
                 .description = "Number of items to generate."}},
               {"resource_name",
                {.type = "string",
                 .required = false,
                 .default_value = Variant(""),
                 .description = "Ignored in stub."}},
               {"bench_profile",
                {.type = "any",
                 .required = false,
                 .default_value = Variant(nullptr),
                 .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(RecallFeedDataStubOp, k_recall_feed_data_schema)

// ─── transform_redis_zrangebyscore ───────────────────────────────────────────
// Stub: reads common input, writes impression_ids (empty array),
// impression_cache_hit (true), impression_ids_len (0).

class TransformRedisZrangebyscoreStubOp : public Operator {
 public:
  void init(const OperatorConfig& cfg) override {
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    (void)input.common("user_id");
    out.set_common("impression_ids", Variant(Variant::array_t{}));
    out.set_common("impression_cache_hit", Variant(true));
    out.set_common("impression_ids_len", Variant(0.0));
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_transform_redis_zrangebyscore_schema{
    .name = "transform_redis_zrangebyscore",
    .type = OpType::Transform,
    .description = "Benchmark stub: simulates Redis ZRANGEBYSCORE.",
    .params =
        {{"key_prefix",
          {.type = "string", .required = false, .default_value = Variant(""), .description = "Stub param."}},
         {"window_seconds",
          {.type = "int", .required = false, .default_value = Variant(0.0), .description = "Stub param."}},
         {"redis_addr",
          {.type = "string", .required = false, .default_value = Variant(""), .description = "Stub param."}},
         {"redis_password",
          {.type = "string", .required = false, .default_value = Variant(""), .description = "Stub param."}},
         {"bench_profile",
          {.type = "any",
           .required = false,
           .default_value = Variant(nullptr),
           .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(TransformRedisZrangebyscoreStubOp, k_transform_redis_zrangebyscore_schema)

// ─── transform_hydrate ──────────────────────────────────────────────────────
// Stub: reads item_input fields, writes creator_id = 0 for each item.

class TransformHydrateStubOp : public Operator, public ConsumesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      (void)input.item(i, "item_id");
      (void)input.item(i, "type");
      out.set_item(static_cast<int>(i), "creator_id", Variant(static_cast<double>(i % 1000)));
    }
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_transform_hydrate_schema{
    .name = "transform_hydrate",
    .type = OpType::Transform,
    .description = "Benchmark stub: simulates MySQL hydration.",
    .params =
        {{"mysql_dsn",
          {.type = "string", .required = false, .default_value = Variant(""), .description = "Stub param."}},
         {"bench_profile",
          {.type = "any",
           .required = false,
           .default_value = Variant(nullptr),
           .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(TransformHydrateStubOp, k_transform_hydrate_schema)

// ─── transform_query_blocked_creators ───────────────────────────────────────
// Stub: reads user_id, writes blocked_creator_ids = [].

class TransformQueryBlockedCreatorsStubOp : public Operator {
 public:
  void init(const OperatorConfig& cfg) override {
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    (void)input.common("user_id");
    (void)input.common("blocked_creator_ids");
    out.set_common("blocked_creator_ids", Variant(Variant::array_t{}));
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_transform_query_blocked_creators_schema{
    .name = "transform_query_blocked_creators",
    .type = OpType::Transform,
    .description = "Benchmark stub: simulates MySQL blocked-creators query.",
    .params =
        {{"mysql_dsn",
          {.type = "string", .required = false, .default_value = Variant(""), .description = "Stub param."}},
         {"bench_profile",
          {.type = "any",
           .required = false,
           .default_value = Variant(nullptr),
           .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(TransformQueryBlockedCreatorsStubOp, k_transform_query_blocked_creators_schema)

// ─── filter_impression ──────────────────────────────────────────────────────
// Stub: removes ~20% of items to simulate impression filtering.

class FilterImpressionStubOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    (void)input.common("impression_ids");
    (void)input.common("size");
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      (void)input.item(i, "item_id");
      (void)input.item(i, "type");
      if (i % 5 == 0) {
        out.remove_item(static_cast<int>(i));
      }
    }
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_filter_impression_schema{
    .name = "filter_impression",
    .type = OpType::Filter,
    .description = "Benchmark stub: simulates impression-based filtering.",
    .params =
        {{"min_remaining_ratio",
          {.type = "float", .required = false, .default_value = Variant(1.5), .description = "Stub param."}},
         {"bench_profile",
          {.type = "any",
           .required = false,
           .default_value = Variant(nullptr),
           .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(FilterImpressionStubOp, k_filter_impression_schema)

// ─── filter_blocked_creator ─────────────────────────────────────────────────
// Stub: reads blocked_creator_ids + creator_id, removes nothing (empty blocklist).

class FilterBlockedCreatorStubOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& input, OperatorOutput& /*out*/) override {
    (void)input.common("blocked_creator_ids");
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      (void)input.item(i, "creator_id");
    }
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_filter_blocked_creator_schema{
    .name = "filter_blocked_creator",
    .type = OpType::Filter,
    .description = "Benchmark stub: simulates blocked-creator filtering.",
    .params = {{"bench_profile",
                {.type = "any",
                 .required = false,
                 .default_value = Variant(nullptr),
                 .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(FilterBlockedCreatorStubOp, k_filter_blocked_creator_schema)

// ─── reorder_topn_boost ──────────────────────────────────────────────────────
// Stub: deterministic top-N boost. Items are ranked by an FNV-1a hash of
// "shuffle_salt | id" (mirroring reorder_shuffle_by_salt); the top `size` items
// by hash are boosted to the front and the rest keep their original order. This
// exercises the row-set reorder path (set_item_order), which a field-only stub
// would not.

class ReorderTopnBoostStubOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
 public:
  void init(const OperatorConfig& cfg) override {
    const auto& params = cfg.params.as_object();
    auto it = params.find("size");
    if (it != params.end() && it->second.is_number()) {
      size_ = static_cast<int>(it->second.as_number());
    }
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    const std::size_t n = input.item_count();
    if (n > 0) {
      const std::string salt_prefix = operators::any_to_string(input.common("shuffle_salt")) + "|";

      struct Ranked {
        std::size_t idx;
        double r;
        uint64_t id;
      };
      std::vector<Ranked> ranked;
      ranked.reserve(n);
      for (std::size_t i = 0; i < n; ++i) {
        std::string item_val = operators::any_to_string(input.item(i, "id"));
        uint64_t h = bench_fnv64a(salt_prefix + item_val);
        double r = static_cast<double>(h) / (static_cast<double>(UINT64_MAX) + 1.0);
        ranked.push_back({i, r, bench_parse_uint64(item_val)});
      }

      std::sort(ranked.begin(), ranked.end(), [](const Ranked& a, const Ranked& b) {
        if (a.r != b.r) {
          return a.r < b.r;
        }
        if (a.id != b.id) {
          return a.id < b.id;
        }
        return a.idx < b.idx;
      });

      std::size_t boost = size_ < 0 ? 0 : static_cast<std::size_t>(size_);
      if (boost > n) {
        boost = n;
      }
      std::vector<bool> boosted(n, false);
      std::vector<int> order;
      order.reserve(n);
      for (std::size_t i = 0; i < boost; ++i) {
        order.push_back(static_cast<int>(ranked[i].idx));
        boosted[ranked[i].idx] = true;
      }
      for (std::size_t i = 0; i < n; ++i) {
        if (!boosted[i]) {
          order.push_back(static_cast<int>(i));
        }
      }
      out.set_item_order(std::move(order));
    }
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  int size_ = 10;
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_reorder_topn_boost_schema{
    .name = "reorder_topn_boost",
    .type = OpType::Reorder,
    .description = "Benchmark stub: simulates top-N boost reordering.",
    .params =
        {{"size",
          {.type = "int", .required = false, .default_value = Variant(10.0), .description = "Stub param."}},
         {"bench_profile",
          {.type = "any",
           .required = false,
           .default_value = Variant(nullptr),
           .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(ReorderTopnBoostStubOp, k_reorder_topn_boost_schema)

// ─── observe_datahub ────────────────────────────────────────────────────────
// Stub: reads all declared input fields (simulating serialization overhead),
// writes nothing.

class ObserveDatahubStubOp : public Operator {
 public:
  void init(const OperatorConfig& cfg) override {
    common_input_ = cfg.metadata.common_input;
    item_input_ = cfg.metadata.item_input;
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& input, OperatorOutput& /*out*/) override {
    for (const auto& k : common_input_) {
      (void)input.common(k);
    }
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      for (const auto& k : item_input_) {
        (void)input.item(i, k);
      }
    }
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  std::vector<std::string> common_input_;
  std::vector<std::string> item_input_;
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_observe_datahub_schema{
    .name = "observe_datahub",
    .type = OpType::Observe,
    .description = "Benchmark stub: simulates DataHub MQ write.",
    .params =
        {{"resource_name",
          {.type = "string", .required = false, .default_value = Variant(""), .description = "Stub param."}},
         {"mode",
          {.type = "string", .required = false, .default_value = Variant(""), .description = "Stub param."}},
         {"key_fields",
          {.type = "array",
           .required = false,
           .default_value = Variant(nullptr),
           .description = "Stub param."}},
         {"bench_profile",
          {.type = "any",
           .required = false,
           .default_value = Variant(nullptr),
           .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(ObserveDatahubStubOp, k_observe_datahub_schema)

// ─── transform_generate_request_id ──────────────────────────────────────────
// Stub: generates a fixed request_id string.

class TransformGenerateRequestIdStubOp : public Operator {
 public:
  void init(const OperatorConfig& cfg) override {
    const auto& params = cfg.params.as_object();
    auto it = params.find("prefix");
    if (it != params.end() && it->second.is_string()) {
      prefix_ = it->second.as_string();
    }
    latency_ = parse_bench_profile(cfg.params);
  }

  void execute(const OperatorInput& /*input*/, OperatorOutput& out) override {
    out.set_common("request_id", Variant(prefix_ + ":550e8400-e29b-41d4-a716-446655440000"));
    if (latency_) {
      volatile double sink = latency_->apply();
      (void)sink;
    }
  }

 private:
  std::string prefix_ = "bench";
  std::unique_ptr<LatencySampler> latency_;
};

static const OperatorSchema k_transform_generate_request_id_schema{
    .name = "transform_generate_request_id",
    .type = OpType::Transform,
    .description = "Benchmark stub: generates a fixed request ID.",
    .params = {{"prefix",
                {.type = "string",
                 .required = false,
                 .default_value = Variant("bench"),
                 .description = "Stub param."}},
               {"bench_profile",
                {.type = "any",
                 .required = false,
                 .default_value = Variant(nullptr),
                 .description = "Latency profile."}}},
};
PINE_REGISTER_OPERATOR_T(TransformGenerateRequestIdStubOp, k_transform_generate_request_id_schema)

}  // namespace pine
