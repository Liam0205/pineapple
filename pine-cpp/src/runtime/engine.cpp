#include "pine/column_frame.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"
#include "pine/template.hpp"

#include <algorithm>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <exception>
#include <functional>
#include <future>
#include <iostream>
#include <map>
#include <memory>
#include <memory_resource>
#include <mutex>
#include <optional>
#include <set>
#include <shared_mutex>
#include <sstream>
#include <stop_token>
#include <thread>

#include "runtime/thread_pool.hpp"

namespace pine {

void OperatorOutput::set_common(const std::string& field, Variant value) {
  common_writes_[field] = std::move(value);
}

void OperatorOutput::set_item(int index, const std::string& field, Variant value) {
  item_writes_.push_back(ItemWrite{index, field, std::move(value)});
}

void OperatorOutput::add_item(Variant::object_t fields) {
  added_items_.push_back(std::move(fields));
}

void OperatorOutput::remove_item(int index) {
  removed_items_.insert(index);
}

void OperatorOutput::set_item_order(std::vector<int> order) {
  item_order_ = std::move(order);
  has_item_order_ = true;
}

void OperatorOutput::set_warning(std::string msg) {
  if (!has_warning_) {
    warning_ = std::move(msg);
    has_warning_ = true;
  }
}

namespace {

// Frame is the polymorphic base now; helpers in this TU still
// use unqualified `Frame` for brevity, which resolves to ::pine::Frame.
using Frame = ::pine::Frame;

bool should_skip(const Frame& frame, const OperatorConfig& op) {
  for (const auto& field : op.skip) {
    Variant v = frame.common(field);
    if (!v.is_null() && v.truthy()) {
      return true;
    }
  }
  return false;
}

Result project_result(const Frame& frame, const FlowContract& contract) {
  return frame.to_result(contract.common_output, contract.item_output);
}

void validate_request(const Request& request, const FlowContract& contract) {
  for (const auto& field : contract.common_input) {
    if (!request.common.count(field)) {
      throw ValidationError("missing required common input field \"" + field + "\"");
    }
  }
  for (std::size_t i = 0; i < request.items.size(); ++i) {
    for (const auto& field : contract.item_input) {
      if (!request.items[i].count(field)) {
        throw ValidationError("item[" + std::to_string(i) + "] missing required item input field \"" + field +
                              "\"");
      }
    }
  }
}

// apply_output is now a member of ColumnFrame (frame.apply_output(out, op_name, is_recall)).
// See pine-cpp/src/dataframe/column_frame.{hpp,cpp} for the canonical
// five-stage application (common writes -> item writes -> removals ->
// reorder -> additions; recall ops stamp `_source = op_name`).

// snapshot_input builds the per-op input view that pine-go records as
// OpTrace.InputSnapshot when debug=true. Includes only declared input fields
// (filtered by skip), with defaults substituted for missing/null values.
// Items section omitted entirely when no item input field has any value.
//
// pine-go derives this from the projected OperatorInput so fields
// that are unset and have no default never appear in the snapshot. The
// earlier C++ version inserted Variant() (null) placeholders for those
// fields, polluting trace output relative to Go.
Variant snapshot_input(const Frame& frame, const OperatorConfig& op) {
  Variant::object_t snap;
  std::set<std::string> skip_set(op.skip.begin(), op.skip.end());

  Variant::object_t common;
  for (const auto& field : op.metadata.common_input) {
    if (skip_set.count(field)) {
      continue;
    }
    Variant v = frame.common(field);
    if (!v.is_null()) {
      common[field] = v;
    } else if (auto def = op.common_defaults.find(field); def != op.common_defaults.end()) {
      common[field] = def->second;
    }
    // else: omit — matches Go BuildInput projection semantics.
  }
  if (!common.empty()) {
    snap["common"] = Variant(std::move(common));
  }

  if (frame.item_count() > 0 && !op.metadata.item_input.empty()) {
    bool has_data = false;
    Variant::array_t items;
    items.reserve(frame.item_count());
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
      Variant::object_t row;
      for (const auto& field : op.metadata.item_input) {
        Variant v = frame.item(i, field);
        if (!v.is_null()) {
          row[field] = v;
        } else if (auto def = op.item_defaults.find(field); def != op.item_defaults.end()) {
          row[field] = def->second;
        }
        // else: omit — matches Go BuildInput projection semantics.
      }
      if (!row.empty()) {
        has_data = true;
      }
      items.push_back(Variant(std::move(row)));
    }
    if (has_data) {
      snap["items"] = Variant(std::move(items));
    }
  }

  return Variant(std::move(snap));
}

// snapshot_output mirrors pine-go's snapshotOutput: serialize the
// OperatorOutput buffer into a stable JSON-friendly shape.
Variant snapshot_output(const OperatorOutput& out) {
  Variant::object_t snap;

  if (!out.common_writes().empty()) {
    Variant::object_t cw;
    for (const auto& [field, value] : out.common_writes()) {
      cw[field] = value;
    }
    snap["common_writes"] = Variant(std::move(cw));
  }

  if (!out.item_writes().empty()) {
    // The vector log can hold multiple writes for the same (idx, field);
    // group by idx and let later writes overwrite earlier ones to
    // preserve the snapshot's "final state" semantics. apply_output
    // also replays the vector in order so the on-frame state matches.
    std::map<int, std::map<std::string, Variant>> grouped;
    for (const auto& w : out.item_writes()) {
      grouped[w.index][w.field] = w.value;
    }
    Variant::object_t iw;
    for (const auto& [idx, fields] : grouped) {
      Variant::object_t row;
      for (const auto& [field, value] : fields) {
        row[field] = value;
      }
      iw[std::to_string(idx)] = Variant(std::move(row));
    }
    snap["item_writes"] = Variant(std::move(iw));
  }

  if (!out.added_items().empty()) {
    Variant::array_t ai;
    ai.reserve(out.added_items().size());
    for (const auto& row : out.added_items()) {
      Variant::object_t obj;
      for (const auto& [field, value] : row) {
        obj[field] = value;
      }
      ai.push_back(Variant(std::move(obj)));
    }
    snap["added_items"] = Variant(std::move(ai));
  }

  if (!out.removed_items().empty()) {
    Variant::array_t ri;
    ri.reserve(out.removed_items().size());
    for (int idx : out.removed_items()) {
      ri.push_back(Variant(static_cast<double>(idx)));
    }
    snap["removed_items"] = Variant(std::move(ri));
  }

  return Variant(std::move(snap));
}

int64_t now_us() {
  using namespace std::chrono;
  return duration_cast<microseconds>(system_clock::now().time_since_epoch()).count();
}

}  // namespace

// Pre-created metrics for the scheduler and per-operator recording.
// Mirrors pine-go internal/runtime/engine_metrics.go EngineMetrics.
struct Engine::EngineMetrics {
  metrics::Counter* scheduler_runs = nullptr;
  metrics::Gauge* active_ops = nullptr;
  metrics::Counter* op_exec_total = nullptr;
  metrics::Histogram* op_exec_duration = nullptr;
  metrics::Counter* op_skip_total = nullptr;
  metrics::Counter* op_error_total = nullptr;
  metrics::Counter* dag_exec_total = nullptr;
  metrics::Histogram* dag_exec_duration = nullptr;
  metrics::Histogram* dag_ops_executed = nullptr;
};

namespace {

std::unique_ptr<Engine::EngineMetrics> build_engine_metrics(metrics::Provider* p,
                                                            const std::vector<std::string>& op_names) {
  auto em = std::make_unique<Engine::EngineMetrics>();
  em->scheduler_runs =
      p->new_counter({"pine_scheduler_runs_total", "Total number of DAG scheduler runs.", {}});
  em->active_ops = p->new_gauge({"pine_operator_active", "Number of operators currently executing.", {}});
  em->op_exec_total =
      p->new_counter({"pine_operator_exec_total", "Total successful operator executions.", {"operator"}});
  em->op_exec_duration = p->new_histogram(
      {{"pine_operator_exec_duration_seconds", "Operator execution duration in seconds.", {"operator"}},
       {0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}});
  em->op_skip_total =
      p->new_counter({"pine_operator_skip_total", "Total skipped operator executions.", {"operator"}});
  em->op_error_total =
      p->new_counter({"pine_operator_error_total", "Total failed operator executions.", {"operator"}});
  em->dag_exec_total = p->new_counter({"pine_dag_executions_total", "Total DAG executions.", {"status"}});
  em->dag_exec_duration =
      p->new_histogram({{"pine_dag_execution_duration_seconds", "DAG execution duration in seconds.", {}},
                        {0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}});
  em->dag_ops_executed =
      p->new_histogram({{"pine_dag_operators_executed", "Number of operators executed per DAG run.", {}},
                        {1, 2, 5, 10, 15, 20, 25, 30, 40, 50, 75, 100, 150, 200, 300, 450}});
  for (const auto& n : op_names) {
    em->op_exec_total->with({n});
    em->op_exec_duration->with({n});
    em->op_skip_total->with({n});
    em->op_error_total->with({n});
  }
  em->dag_exec_total->with({"success"});
  em->dag_exec_total->with({"error"});
  return em;
}

}  // namespace

// Engine::PoolHolder wraps the worker pool, hidden from pine.hpp so the
// public header does not depend on runtime/thread_pool.hpp.
struct Engine::PoolHolder {
  runtime::ThreadPool pool;
  explicit PoolHolder(std::size_t n) : pool(n) {
  }
};

// TemplatedPlans holds the per-operator {{field}} interpolation plan
// computed at Engine construction. The resolved map is attached to the
// per-request OperatorInput by the scheduler; no operator-side opt-in
// interface is required.
struct Engine::TemplatedPlans {
  struct Entry {
    std::vector<TemplatedParam> plan;
  };
  std::map<std::string, Entry> by_op;
};

Engine::Engine(Config config) : Engine(std::move(config), EngineOptions{}) {
}

Engine::~Engine() = default;
Engine::Engine(Engine&&) noexcept = default;
Engine& Engine::operator=(Engine&&) noexcept = default;

Engine::Engine(Config config, EngineOptions options) : config_(std::move(config)) {
  bool global_debug = options.debug.has_value() ? *options.debug : config_.debug;
  // Tri-state debug: op-level debug overrides global when explicitly set
  for (auto& [_, op] : config_.operators) {
    if (!op.debug.has_value()) {
      op.debug = global_debug;
    }
  }
  log_prefix_ = options.log_prefix.has_value() ? *options.log_prefix : config_.log_prefix;
  peak_concurrency_ = std::make_unique<std::atomic<int64_t>>(0);
  metrics_provider_ = options.metrics_provider ? options.metrics_provider : metrics::nop_provider();
  resource_provider_ = options.resource_provider;
  {
    unsigned hw = std::thread::hardware_concurrency();
    if (hw == 0) {
      hw = 4;
    }
    std::size_t dag_size = options.dag_pool_size.value_or(static_cast<std::size_t>(hw) * 4);
    std::size_t shard_size = options.shard_pool_size.value_or(static_cast<std::size_t>(hw) * 2);
    dag_pool_ = std::make_unique<PoolHolder>(dag_size);
    shard_pool_ = std::make_unique<PoolHolder>(shard_size);
  }
  expanded_ = expand_operator_sequence_with_subflows(config_);
  graph_ = build_dag(config_, expanded_);
  engine_metrics_ = build_engine_metrics(metrics_provider_, expanded_.sequence);

  // Forward-reference validation runs inside build_dag() above
  // (dag.cpp:155 raises ConfigError("operator \"X\": sources contains
  // forward reference to \"Y\"")), so by the time we reach this point
  // every operator's sources are guaranteed to refer to nodes already
  // visited. A second native-side check would be dead code.

  // Instantiate and init one Operator per config operator.
  templated_plans_ = std::make_unique<TemplatedPlans>();
  for (auto& [op_name, op_cfg] : config_.operators) {
    const auto* entry = registry_entry(op_cfg.type_name);
    if (!entry || !entry->factory) {
      throw RegistryError("operator \"" + op_name + "\": operator type not registered: \"" +
                          op_cfg.type_name + "\"");
    }
    auto instance = entry->factory();
    instance->init(op_cfg);
    // Inject metrics provider for operators that opt-in. Mirrors
    // pine-go pine.go:170 — init first, then provider injection.
    if (auto* ma = dynamic_cast<MetricsAware*>(instance.get())) {
      ma->set_metrics_provider(metrics_provider_);
    }
    // Inject resource provider for operators that borrow handle-typed
    // resources by name. May be null (no provider configured), in which case
    // the operator degrades at execute time. Same ordering as metrics: init
    // first, then provider injection.
    if (auto* ra = dynamic_cast<ResourceAware*>(instance.get())) {
      ra->set_resource_provider(resource_provider_);
    }
    // Compute the {{field}} interpolation plan for this operator
    // (issue #74). Build-time errors carry the canonical
    // `operator "X": ...` prefix; runtime errors don't, since
    // dispatch_with_recovery re-wraps via ExecutionError. The Apple
    // compiler has already injected the referenced common fields into
    // common_input, so DAG dependencies guarantee the values exist
    // by the time this operator runs (mirrors how `if_` skip-field
    // dependencies are wired). The resolved map is attached to the
    // per-request OperatorInput; operators read it via
    // input.templated_param(name).
    std::vector<TemplatedParam> plan = build_templated_param_plan(op_name, entry->schema, op_cfg.params);
    if (!plan.empty()) {
      TemplatedPlans::Entry pe;
      pe.plan = std::move(plan);
      templated_plans_->by_op.emplace(op_name, std::move(pe));
    }
    operators_.emplace(op_name, std::move(instance));
    // Pre-compute InputFieldSpec for the BuildInput projection layer.
    input_specs_.emplace(op_name, compute_input_field_spec(op_cfg));
  }
}

Engine Engine::from_file(const std::string& path) {
  return Engine(load_config_from_file(path));
}
Engine Engine::from_file(const std::string& path, EngineOptions options) {
  return Engine(load_config_from_file(path), std::move(options));
}

namespace {

void dispatch_operator(const OperatorInput& input, const OperatorConfig& op,
                       const std::map<std::string, std::unique_ptr<Operator>>& operators,
                       OperatorOutput& out) {
  auto it = operators.find(op.name);
  if (it == operators.end() || !it->second) {
    throw RegistryError("operator \"" + op.name + "\": operator type not registered: \"" + op.type_name +
                        "\"");
  }
  it->second->execute(input, out);
}

// validate_output_against_type mirrors pine-go types.OperatorType.ValidateOutput
// (operator.go:57-150). After an operator runs, the engine checks that its
// OperatorOutput only used the methods allowed for its declared OperatorType
// — e.g. a Recall must not SetCommon/SetItem/RemoveItem/SetItemOrder.
// Violations throw a two-arg ExecutionError(op_name, "type violation: ..."),
// matching Go scheduler.go:172 which wraps the bare violation with
// `fmt.Errorf("type violation: %w", vErr)` before the standard
// `pine: execution error in operator "X": ...` prefix layer.
//
// Error format (Go-compatible byte-exact): `type violation: operator type X
// must not call [Method1 Method2]`. The bracketed list mimics Go's `%v`
// formatting of a []string (single space separator, no quotes).
void validate_output_against_type(const std::string& op_name, const std::string& op_type,
                                  const OperatorOutput& out) {
  const bool has_cw = !out.common_writes().empty();
  const bool has_iw = !out.item_writes().empty();
  const bool has_ai = !out.added_items().empty();
  const bool has_ri = !out.removed_items().empty();
  const bool has_io = out.has_item_order();

  std::vector<std::string> violations;
  // Note: op_type comes from OperatorConfig::operator_type which is the
  // lowercase form ("recall" / "transform" / ...). pine-go's OperatorType
  // is PascalCase ("Recall") and gets formatted into the error verbatim,
  // so we need the PascalCase form here for byte-exact parity.
  std::string display_type;
  if (op_type == "recall") {
    display_type = "Recall";
    if (has_cw) {
      violations.push_back("SetCommon");
    }
    if (has_iw) {
      violations.push_back("SetItem");
    }
    if (has_ri) {
      violations.push_back("RemoveItem");
    }
    if (has_io) {
      violations.push_back("SetItemOrder");
    }
  } else if (op_type == "transform") {
    display_type = "Transform";
    if (has_ai) {
      violations.push_back("AddItem");
    }
    if (has_ri) {
      violations.push_back("RemoveItem");
    }
    if (has_io) {
      violations.push_back("SetItemOrder");
    }
  } else if (op_type == "filter") {
    display_type = "Filter";
    if (has_cw) {
      violations.push_back("SetCommon");
    }
    if (has_iw) {
      violations.push_back("SetItem");
    }
    if (has_ai) {
      violations.push_back("AddItem");
    }
    if (has_io) {
      violations.push_back("SetItemOrder");
    }
  } else if (op_type == "merge") {
    display_type = "Merge";
    if (has_cw) {
      violations.push_back("SetCommon");
    }
    if (has_ai) {
      violations.push_back("AddItem");
    }
    if (has_io) {
      violations.push_back("SetItemOrder");
    }
  } else if (op_type == "reorder") {
    display_type = "Reorder";
    if (has_cw) {
      violations.push_back("SetCommon");
    }
    if (has_iw) {
      violations.push_back("SetItem");
    }
    if (has_ai) {
      violations.push_back("AddItem");
    }
    if (has_ri) {
      violations.push_back("RemoveItem");
    }
  } else if (op_type == "observe") {
    display_type = "Observe";
    if (has_cw) {
      violations.push_back("SetCommon");
    }
    if (has_iw) {
      violations.push_back("SetItem");
    }
    if (has_ai) {
      violations.push_back("AddItem");
    }
    if (has_ri) {
      violations.push_back("RemoveItem");
    }
    if (has_io) {
      violations.push_back("SetItemOrder");
    }
  } else {
    return;  // unknown type — earlier validation should already have failed
  }

  if (violations.empty()) {
    return;
  }
  std::string list = "[";
  for (std::size_t i = 0; i < violations.size(); ++i) {
    if (i > 0) {
      list += " ";
    }
    list += violations[i];
  }
  list += "]";
  throw ExecutionError(op_name, "type violation: operator type " + display_type + " must not call " + list);
}

// dispatch_with_recovery runs dispatch_operator and converts any non-pine::Error
// exception into a PanicError carrying the operator name. Pine typed errors
// (ExecutionError, RegistryError, etc.) propagate unchanged after ensuring
// they are correctly formatted matching Go.
//
// Both re-wrap paths use std::throw_with_nested so the original in-flight
// exception is preserved as a nested cause on the outgoing ExecutionError /
// PanicError. Downstream code can use pine::error_as<T>() to walk the chain,
// mirroring Go's errors.As / Java's Throwable.getCause().
void dispatch_with_recovery(const OperatorInput& input, const OperatorConfig& op,
                            const std::map<std::string, std::unique_ptr<Operator>>& operators,
                            OperatorOutput& out) {
  try {
    dispatch_operator(input, op, operators, out);
  } catch (ExecutionError& e) {
    // If the execution error was thrown without operator name context or
    // formatted with raw inner message, ensure it matches:
    // `pine: execution error in operator "X": <inner>`
    if (e.operator_name().empty()) {
      std::throw_with_nested(ExecutionError(op.name, e.inner().empty() ? std::string(e.what()) : e.inner()));
    }
    throw;
  } catch (const Error&) {
    throw;
  } catch (const std::exception& e) {
    std::throw_with_nested(PanicError(op.name, e.what()));
  } catch (...) {
    // Non-std::exception payloads (`throw 42;`, `throw "literal";`) are
    // rare but legal. throw_with_nested still captures them so
    // pine::error_as<T>() can at least walk the chain even if no frame
    // dynamic_casts to a useful type.
    std::throw_with_nested(PanicError(op.name, "unknown exception"));
  }
}

// merge_shard_output merges a shard's OperatorOutput into the master output,
// applying `offset` to item-index references. Parallel ops are constrained to
// transforms (no added_items, no item_order, empty common_output), so only
// item_writes, removed_items, and warnings need merging.
//
// Runtime assert: if a shard *does* emit added_items / item_order / common
// writes (which would happen only if config validation or operator schema
// was bypassed), fail loudly rather than silently dropping data — the
// reviewer-flagged silent-drop path.
void merge_shard_output(OperatorOutput& dst, const OperatorOutput& src, int offset,
                        const std::string& op_name) {
  if (!src.added_items().empty() || src.has_item_order() || !src.common_writes().empty()) {
    // Message body must match pine-go/parallel.go:65, pine-java
    // ParallelExecutor.java:85, and pine-python parallel.py:86-88
    // byte-for-byte — PanicError values are part of the cross-runtime
    // contract (memory: "运行时错误对等需字节级一致").
    throw PanicError(op_name,
                     "data_parallel shard emitted added_items, item_order, or common "
                     "writes; only item_writes / removed_items / warnings are allowed");
  }
  for (const auto& [idx, field, value] : src.item_writes()) {
    dst.set_item(idx + offset, field, value);
  }
  for (int idx : src.removed_items()) {
    dst.remove_item(idx + offset);
  }
  if (src.has_warning()) {
    dst.set_warning(src.warning());
  }
}

// parallel_execute shards frame.items across op.data_parallel workers, executes
// the operator concurrently on each shard, and merges shard OperatorOutputs
// back into `out` with index offsets. Mirrors pine-go's runtime/parallel.go.
//
// Preconditions (enforced by config validation):
//   - op.operator_type == "transform"
//   - op.metadata.common_output is empty
//   - op.type_name is in the ConcurrentSafe set
// Therefore shards only emit item_writes / removed_items / warnings.
//
// When `pool` is non-null shard tasks are dispatched through the Engine's
// shared ThreadPool, avoiding the per-request OS-thread spawn cost that
// pine-go gets for free through goroutines. When `pool` is null
// the legacy per-shard std::thread fallback runs — kept so unit tests and
// stand-alone callers that construct Frame directly still work.
void parallel_execute(const Frame& frame, const OperatorConfig& op,
                      const std::map<std::string, std::unique_ptr<Operator>>& operators, OperatorOutput& out,
                      const InputFieldSpec& spec,
                      const Engine::TemplatedPlans::Entry* templated_entry = nullptr,
                      runtime::ThreadPool* pool = nullptr, detail::CentralArena* central = nullptr) {
  // Per-request {{field}} interpolation (issue #74). Resolved once
  // against the parent frame, then attached to every OperatorInput we
  // build below — single-shard input or per-shard inputs alike — by
  // const-pointer (no copy). The resolved map lives in this stack
  // frame, which outlives every shard (we join before returning).
  // resolve_templated_params throws ExecutionError (no op prefix) on
  // missing field or coerce failure; dispatch_with_recovery's nested
  // throw wrap adds the byte-exact prefix.
  std::unordered_map<std::string, Variant> resolved;
  const std::unordered_map<std::string, Variant>* resolved_ptr = nullptr;
  if (templated_entry && !templated_entry->plan.empty()) {
    OperatorInput probe = build_operator_input(frame, op.name, spec);
    resolved = resolve_templated_params(op.name, templated_entry->plan, probe);
    resolved_ptr = &resolved;
  }
  int total = static_cast<int>(frame.item_count());
  int n = op.data_parallel;
  if (n <= 1 || total == 0) {
    OperatorInput input = build_operator_input(frame, op.name, spec);
    input.set_templated_params(resolved_ptr);
    dispatch_with_recovery(input, op, operators, out);
    return;
  }
  if (n > total) {
    n = total;
  }

  int base = total / n;
  int rem = total % n;

  // Build shards as zero-copy window views into the parent frame's
  // ColumnStore. Previously each shard materialised its rows
  // into a fresh row-major list and then back into a per-shard
  // ColumnStore — costing 2 × column-cell touches per request just to
  // set up parallelism. The window view shares parent storage with an
  // (offset, count) translation; the parent is read-only during the
  // shard window (no apply_output until merge_shard_output below).
  std::vector<std::unique_ptr<Frame>> shards;
  shards.reserve(static_cast<std::size_t>(n));
  std::vector<OperatorOutput> shard_outs(static_cast<std::size_t>(n));
  std::vector<int> offsets(static_cast<std::size_t>(n));
  int cursor = 0;
  for (int i = 0; i < n; ++i) {
    int size = base + (i < rem ? 1 : 0);
    auto shard = frame.make_window_view(static_cast<std::size_t>(cursor), static_cast<std::size_t>(size));
    shards.push_back(std::move(shard));
    offsets[static_cast<std::size_t>(i)] = cursor;
    cursor += size;
  }

  std::mutex err_mu;
  std::exception_ptr first_err;
  // shard-level cancellation: first shard to fail flips this flag so
  // un-started shards bail out before invoking dispatch_with_recovery.
  // Mirrors pine-go parallel.go:118 — `cancel()` after errOnce.Do.
  // Shards already running cannot be interrupted mid-execute (operators
  // are not designed to be re-entrant against cancel during their own
  // execute body), so the saving is for shards still queued in the
  // ThreadPool / awaiting their thread slot.
  std::atomic<bool> shard_cancel{false};

  auto shard_body = [&](int i) {
    if (shard_cancel.load(std::memory_order_acquire)) {
      return;
    }
    std::optional<ScopedInstall> guard;
    if (central) {
      guard.emplace(central);
    }
    try {
      OperatorInput input = build_operator_input(*shards[static_cast<std::size_t>(i)], op.name, spec);
      input.set_templated_params(resolved_ptr);
      dispatch_with_recovery(input, op, operators, shard_outs[static_cast<std::size_t>(i)]);
    } catch (...) {
      std::lock_guard<std::mutex> lk(err_mu);
      if (!first_err) {
        first_err = std::current_exception();
        shard_cancel.store(true, std::memory_order_release);
      }
    }
  };

  if (pool != nullptr) {
    std::vector<std::future<void>> futs;
    futs.reserve(static_cast<std::size_t>(n));
    for (int i = 0; i < n; ++i) {
      futs.push_back(pool->submit([&, i]() { shard_body(i); }));
    }
    for (auto& f : futs) {
      f.wait();
    }
  } else {
    std::vector<std::thread> threads;
    threads.reserve(static_cast<std::size_t>(n));
    for (int i = 0; i < n; ++i) {
      threads.emplace_back([&, i]() { shard_body(i); });
    }
    for (auto& t : threads) {
      t.join();
    }
  }

  if (first_err) {
    std::rethrow_exception(first_err);
  }

  for (int i = 0; i < n; ++i) {
    merge_shard_output(out, shard_outs[static_cast<std::size_t>(i)], offsets[static_cast<std::size_t>(i)],
                       op.name);
  }
}

// run_dag executes the DAG using a ready-queue scheduler: only nodes whose
// predecessors have all completed are submitted to the DAG thread pool.
// When a node finishes, it decrements the in-degree of its successors and
// submits any that become ready. The pool never holds blocked tasks.
std::vector<OpTrace> run_dag(const Config& config, const Graph& graph,
                             const std::map<std::string, std::unique_ptr<Operator>>& operators,
                             const std::map<std::string, InputFieldSpec>& input_specs,
                             const Engine::TemplatedPlans* templated_plans, Frame& frame, bool collect_traces,
                             std::atomic<int64_t>* peak_concurrency = nullptr,
                             Engine::EngineMetrics* em = nullptr, runtime::ThreadPool* dag_pool = nullptr,
                             runtime::ThreadPool* shard_pool = nullptr,
                             std::stop_token external_cancel = std::stop_token{},
                             detail::CentralArena* central = nullptr) {
  const std::size_t n = graph.nodes.size();

  if (em && em->scheduler_runs) {
    em->scheduler_runs->inc();
  }
  auto dag_start = std::chrono::steady_clock::now();
  std::atomic<int64_t> ops_executed{0};

  std::vector<OpTrace> traces;
  if (collect_traces) {
    traces.assign(n, OpTrace{});
  }

  std::mutex fatal_mu;
  std::exception_ptr fatal_err;
  std::atomic<int64_t> active_ops{0};

  std::stop_source cancel_source;
  auto stop_token = cancel_source.get_token();

  std::optional<std::stop_callback<std::function<void()>>> external_link;
  if (external_cancel.stop_possible()) {
    external_link.emplace(external_cancel,
                          std::function<void()>([&cancel_source] { cancel_source.request_stop(); }));
  }

  auto fail = [&](std::exception_ptr e) {
    std::lock_guard<std::mutex> lk(fatal_mu);
    if (!fatal_err) {
      fatal_err = e;
      cancel_source.request_stop();
    }
  };

  auto record_peak = [&](int64_t n_current) {
    if (!peak_concurrency) {
      return;
    }
    for (;;) {
      int64_t cur = peak_concurrency->load(std::memory_order_relaxed);
      if (n_current <= cur) {
        return;
      }
      if (peak_concurrency->compare_exchange_weak(cur, n_current, std::memory_order_relaxed)) {
        return;
      }
    }
  };

  // In-degree tracking: each entry starts at the number of predecessors.
  // When a predecessor completes, it atomically decrements successors'
  // in-degree; reaching zero means the node is ready to run.
  auto in_degree = std::make_unique<std::atomic<int>[]>(n);
  for (std::size_t i = 0; i < n; ++i) {
    in_degree[i].store(static_cast<int>(graph.nodes[i].preds.size()), std::memory_order_relaxed);
  }

  // Completion latch: counts down from n. When it reaches 0, all nodes
  // have finished (either executed or skipped due to cancellation).
  std::atomic<std::size_t> remaining{n};
  std::mutex done_mu;
  std::condition_variable done_cv;

  std::function<void(std::size_t)> node_body;

  // propagate_and_signal: after a node finishes (success, skip, or cancel),
  // decrement successors' in-degree and submit newly-ready ones, then
  // decrement `remaining` and notify the main thread if we're the last.
  auto propagate_and_signal = [&](std::size_t i) {
    for (int succ : graph.nodes[i].succs) {
      auto su = static_cast<std::size_t>(succ);
      int prev = in_degree[su].fetch_sub(1, std::memory_order_acq_rel);
      if (prev == 1) {
        if (dag_pool) {
          try {
            dag_pool->submit([&, su]() { node_body(su); });
          } catch (...) {
            node_body(su);
          }
        } else {
          node_body(su);
        }
      }
    }
    if (remaining.fetch_sub(1, std::memory_order_acq_rel) == 1) {
      std::lock_guard<std::mutex> lk(done_mu);
      done_cv.notify_all();
    }
  };

  node_body = [&](std::size_t i) {
    if (stop_token.stop_requested()) {
      propagate_and_signal(i);
      return;
    }

    std::optional<ScopedInstall> guard;
    if (central) {
      guard.emplace(central);
    }

    const auto& node = graph.nodes[i];
    const auto& op = config.operators.at(node.name);

    int64_t cur_active = active_ops.fetch_add(1, std::memory_order_relaxed) + 1;
    record_peak(cur_active);
    if (em && em->active_ops) {
      em->active_ops->set(static_cast<double>(cur_active));
    }

    OpTrace trace;
    if (collect_traces) {
      trace.name = op.name;
      trace.start_time_us = now_us();
    }
    auto start = std::chrono::steady_clock::now();

    OperatorOutput out;

    try {
      bool skip = should_skip(frame, op);
      if (skip) {
        if (em && em->op_skip_total) {
          em->op_skip_total->with({op.name})->inc();
        }
        if (collect_traces) {
          auto end = std::chrono::steady_clock::now();
          trace.duration_us = std::chrono::duration_cast<std::chrono::microseconds>(end - start).count();
          trace.skipped = true;
          traces[i] = std::move(trace);
        }
        int64_t after = active_ops.fetch_sub(1, std::memory_order_relaxed) - 1;
        if (em && em->active_ops) {
          em->active_ops->set(static_cast<double>(after));
        }
        propagate_and_signal(i);
        return;
      }

      if (collect_traces && op.debug.value_or(false)) {
        trace.input_snapshot = snapshot_input(frame, op);
        trace.has_input_snapshot = true;
      }
      parallel_execute(frame, op, operators, out, input_specs.at(op.name),
                       templated_plans ? [&]() -> const Engine::TemplatedPlans::Entry* {
                         auto it = templated_plans->by_op.find(op.name);
                         return it == templated_plans->by_op.end() ? nullptr : &it->second;
                       }()
                                       : nullptr,
                       shard_pool, central);
      if (collect_traces && op.debug.value_or(false)) {
        trace.output_snapshot = snapshot_output(out);
        trace.has_output_snapshot = true;
      }
      validate_output_against_type(op.name, op.operator_type, out);

      if (op.debug.value_or(false)) {
        auto dur = std::chrono::steady_clock::now() - start;
        auto nanos = std::chrono::duration_cast<std::chrono::nanoseconds>(dur).count();
        std::string dur_str;
        if (nanos < 1'000'000) {
          dur_str = std::to_string(nanos / 1000.0) + "µs";
        } else {
          dur_str = std::to_string(nanos / 1'000'000.0) + "ms";
        }
        std::size_t input_size = frame.item_count();
        std::size_t output_size = input_size + out.added_items().size() - out.removed_items().size();
        std::string in_json =
            trace.has_input_snapshot ? dump_json(trace.input_snapshot, 0) : std::string("{}");
        std::string out_json =
            trace.has_output_snapshot ? dump_json(trace.output_snapshot, 0) : std::string("{}");
        while (!in_json.empty() && in_json.back() == '\n') {
          in_json.pop_back();
        }
        while (!out_json.empty() && out_json.back() == '\n') {
          out_json.pop_back();
        }
        std::cerr << "[pine-debug] operator=\"" << op.name << "\" duration=" << dur_str
                  << " input_size=" << input_size << " output_size=" << output_size << " input=" << in_json
                  << " output=" << out_json << "\n";
      }
      frame.apply_output(out, op.name, op.operator_type == "recall");
      auto end = std::chrono::steady_clock::now();
      auto dur_ns = std::chrono::duration_cast<std::chrono::nanoseconds>(end - start);
      if (em) {
        if (em->op_exec_total) {
          em->op_exec_total->with({op.name})->inc();
        }
        if (em->op_exec_duration) {
          em->op_exec_duration->with({op.name})->observe(metrics::duration_seconds(dur_ns));
        }
      }
      ops_executed.fetch_add(1, std::memory_order_relaxed);
      if (collect_traces) {
        trace.duration_us = std::chrono::duration_cast<std::chrono::microseconds>(end - start).count();
        traces[i] = std::move(trace);
      }
    } catch (...) {
      if (em && em->op_error_total) {
        em->op_error_total->with({op.name})->inc();
      }
      if (out.has_warning()) {
        try {
          frame.push_warning(std::string("operator \"") + op.name + "\": " + out.warning());
        } catch (...) {
        }
      }
      fail(std::current_exception());
    }
    // Decrement active ops BEFORE signaling completion — after
    // propagate_and_signal, run_dag may return and destroy locals.
    {
      int64_t after = active_ops.fetch_sub(1, std::memory_order_relaxed) - 1;
      if (em && em->active_ops) {
        em->active_ops->set(static_cast<double>(after));
      }
    }
    propagate_and_signal(i);
  };

  // Seed the ready queue with all root nodes (in-degree == 0).
  // Use graph.nodes[i].preds.size() (immutable) instead of in_degree[i]
  // (mutable atomic) — pool workers may have already decremented in_degree
  // for non-root nodes by the time this loop reaches them.
  for (std::size_t i = 0; i < n; ++i) {
    if (graph.nodes[i].preds.empty()) {
      if (dag_pool) {
        try {
          dag_pool->submit([&, i]() { node_body(i); });
        } catch (...) {
          node_body(i);
        }
      } else {
        node_body(i);
      }
    }
  }

  // Wait for all nodes to complete.
  {
    std::unique_lock<std::mutex> lk(done_mu);
    done_cv.wait(lk, [&] { return remaining.load(std::memory_order_acquire) == 0; });
  }

  auto dag_end = std::chrono::steady_clock::now();
  if (em) {
    if (em->dag_exec_duration) {
      em->dag_exec_duration->observe(metrics::duration_seconds(
          std::chrono::duration_cast<std::chrono::nanoseconds>(dag_end - dag_start)));
    }
    if (em->dag_ops_executed) {
      em->dag_ops_executed->observe(static_cast<double>(ops_executed.load(std::memory_order_relaxed)));
    }
    if (em->dag_exec_total) {
      em->dag_exec_total->with({fatal_err ? "error" : "success"})->inc();
    }
  }

  if (fatal_err) {
    std::rethrow_exception(fatal_err);
  }

  std::vector<OpTrace> filtered;
  filtered.reserve(traces.size());
  for (auto& t : traces) {
    if (!t.name.empty()) {
      filtered.push_back(std::move(t));
    }
  }
  return filtered;
}

}  // namespace

Result Engine::execute(const Request& request) const {
  static const std::map<std::string, Variant> empty_resources;
  return execute(request, empty_resources, std::stop_token{});
}

Result Engine::execute(const Request& request, const std::map<std::string, Variant>& resources) const {
  return execute(request, resources, std::stop_token{});
}

Result Engine::execute(const Request& request, const std::map<std::string, Variant>& resources,
                       std::stop_token external_cancel) const {
  // Route through the traced path so partial-result salvage runs even
  // for callers using the no-trace API. Mirrors pine-go's Execute()
  // contract: `(*Result, error)` — partial Result survives errors. On
  // exception the caller still loses access to the partial Result via
  // this overload (no out-parameter in the signature); to access the
  // partial Result and warnings, use execute_traced_into.
  TracedResult traced;
  execute_traced_into(request, resources, &traced, external_cancel);
  return std::move(traced.result);
}

TracedResult Engine::execute_traced(const Request& request,
                                    const std::map<std::string, Variant>& resources) const {
  TracedResult traced;
  execute_traced_into(request, resources, &traced);
  return traced;
}

void Engine::execute_traced_into(const Request& request, const std::map<std::string, Variant>& resources,
                                 TracedResult* out) const {
  execute_traced_into(request, resources, out, std::stop_token{});
}

void Engine::execute_traced_into(const Request& request, const std::map<std::string, Variant>& resources,
                                 TracedResult* out, std::stop_token external_cancel) const {
  validate_request(request, config_.flow_contract);
  // Capture the calling thread's central arena (non-null if RequestArena is active).
  // Worker threads get their own ThreadLocalBump backed by this central arena.
  auto* central = current_central_arena();
  // Frame is now the polymorphic base; pick the implementation
  // requested by storage_mode ("column" / "row"), default "column".
  std::unique_ptr<Frame> frame_ptr = make_frame(config_.storage_mode, request.common, request.items);
  Frame& frame = *frame_ptr;
  frame.set_resources(&resources);
  std::exception_ptr run_err = nullptr;
  try {
    out->trace = run_dag(config_, graph_, operators_, input_specs_, templated_plans_.get(), frame,
                         /*collect_traces=*/true, peak_concurrency_.get(), engine_metrics_.get(),
                         dag_pool_ ? &dag_pool_->pool : nullptr, shard_pool_ ? &shard_pool_->pool : nullptr,
                         external_cancel, central);
  } catch (...) {
    run_err = std::current_exception();
  }
  // Match Go: even on execution failure we project the partial Result and take warnings
  out->result = project_result(frame, config_.flow_contract);
  out->warnings = frame.take_warnings();
  if (run_err) {
    std::rethrow_exception(run_err);
  }
}

std::string Engine::render_dag(const std::string& format, int collapse) const {
  if (format == "dot") {
    return collapse > 0 ? render_collapsed_dot(graph_, collapse) : render_dot(graph_);
  }
  if (format == "mermaid") {
    return collapse > 0 ? render_collapsed_mermaid(graph_, collapse) : render_mermaid(graph_);
  }
  throw ValidationError("unsupported DAG format \"" + format + "\" (use \"dot\" or \"mermaid\")");
}

int64_t Engine::peak_concurrency() const {
  return peak_concurrency_ ? peak_concurrency_->load(std::memory_order_relaxed) : 0;
}

std::map<std::string, std::map<std::string, int64_t>> Engine::operator_custom_stats() const {
  std::map<std::string, std::map<std::string, int64_t>> result;
  for (const auto& cop : expanded_.sequence) {
    auto it = operators_.find(cop);  // cop is std::string
    if (it != operators_.end()) {
      if (auto* sp = dynamic_cast<const StatsProvider*>(it->second.get())) {
        auto s = sp->operator_stats();
        if (!s.empty()) {
          result[cop] = std::move(s);
        }
      }
    }
  }
  return result;
}

void Engine::close() {
  for (auto& [name, instance] : operators_) {
    if (auto* c = dynamic_cast<Closer*>(instance.get())) {
      c->close();
    }
  }
}

}  // namespace pine
