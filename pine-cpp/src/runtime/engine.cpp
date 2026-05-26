#include "pine/pine.hpp"
#include "pine/column_frame.hpp"
#include "pine/operator.hpp"
#include "runtime/thread_pool.hpp"

#include <algorithm>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <exception>
#include <future>
#include <iostream>
#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <shared_mutex>
#include <sstream>
#include <stop_token>
#include <thread>

namespace pine {

void OperatorOutput::set_common(const std::string& field, JsonValue value) {
    common_writes_[field] = std::move(value);
}

void OperatorOutput::set_item(int index, const std::string& field, JsonValue value) {
    item_writes_[index][field] = std::move(value);
}

void OperatorOutput::add_item(std::map<std::string, JsonValue> fields) {
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

using Frame = ColumnFrame;

bool should_skip(const Frame& frame, const OperatorConfig& op) {
    for (const auto& field : op.skip) {
        JsonValue v = frame.common(field);
        if (!v.is_null() && v.truthy()) return true;
    }
    return false;
}

Result project_result(const Frame& frame, const FlowContract& contract) {
    return frame.to_result(contract.common_output, contract.item_output);
}

void validate_request(const Request& request, const FlowContract& contract) {
    for (const auto& field : contract.common_input) if (!request.common.count(field)) throw ValidationError("missing required common input field \"" + field + "\"");
    for (std::size_t i = 0; i < request.items.size(); ++i) {
        for (const auto& field : contract.item_input) if (!request.items[i].count(field)) throw ValidationError("item[" + std::to_string(i) + "] missing required item input field \"" + field + "\"");
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
JsonValue snapshot_input(const Frame& frame, const OperatorConfig& op) {
    JsonValue::object_t snap;
    std::set<std::string> skip_set(op.skip.begin(), op.skip.end());

    JsonValue::object_t common;
    for (const auto& field : op.metadata.common_input) {
        if (skip_set.count(field)) continue;
        JsonValue v = frame.common(field);
        if (!v.is_null()) {
            common[field] = v;
        } else if (auto def = op.common_defaults.find(field); def != op.common_defaults.end()) {
            common[field] = def->second;
        } else {
            common[field] = JsonValue();
        }
    }
    if (!common.empty()) snap["common"] = JsonValue(std::move(common));

    if (frame.item_count() > 0 && !op.metadata.item_input.empty()) {
        bool has_data = false;
        JsonValue::array_t items;
        items.reserve(frame.item_count());
        for (std::size_t i = 0; i < frame.item_count(); ++i) {
            JsonValue::object_t row;
            for (const auto& field : op.metadata.item_input) {
                JsonValue v = frame.item(i, field);
                if (!v.is_null()) {
                    row[field] = v;
                } else if (auto def = op.item_defaults.find(field); def != op.item_defaults.end()) {
                    row[field] = def->second;
                } else {
                    row[field] = JsonValue();
                }
            }
            if (!row.empty()) has_data = true;
            items.push_back(JsonValue(std::move(row)));
        }
        if (has_data) snap["items"] = JsonValue(std::move(items));
    }

    return JsonValue(std::move(snap));
}

// snapshot_output mirrors pine-go's snapshotOutput: serialize the
// OperatorOutput buffer into a stable JSON-friendly shape.
JsonValue snapshot_output(const OperatorOutput& out) {
    JsonValue::object_t snap;

    if (!out.common_writes().empty()) {
        JsonValue::object_t cw;
        for (const auto& [field, value] : out.common_writes()) cw[field] = value;
        snap["common_writes"] = JsonValue(std::move(cw));
    }

    if (!out.item_writes().empty()) {
        JsonValue::object_t iw;
        for (const auto& [idx, fields] : out.item_writes()) {
            JsonValue::object_t row;
            for (const auto& [field, value] : fields) row[field] = value;
            iw[std::to_string(idx)] = JsonValue(std::move(row));
        }
        snap["item_writes"] = JsonValue(std::move(iw));
    }

    if (!out.added_items().empty()) {
        JsonValue::array_t ai;
        ai.reserve(out.added_items().size());
        for (const auto& row : out.added_items()) {
            JsonValue::object_t obj;
            for (const auto& [field, value] : row) obj[field] = value;
            ai.push_back(JsonValue(std::move(obj)));
        }
        snap["added_items"] = JsonValue(std::move(ai));
    }

    if (!out.removed_items().empty()) {
        JsonValue::array_t ri;
        ri.reserve(out.removed_items().size());
        for (int idx : out.removed_items()) ri.push_back(JsonValue(static_cast<double>(idx)));
        snap["removed_items"] = JsonValue(std::move(ri));
    }

    return JsonValue(std::move(snap));
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
    em->scheduler_runs = p->new_counter({"pine_scheduler_runs_total", "Total number of DAG scheduler runs.", {}});
    em->active_ops = p->new_gauge({"pine_operator_active", "Number of operators currently executing.", {}});
    em->op_exec_total = p->new_counter({"pine_operator_exec_total", "Total successful operator executions.", {"operator"}});
    em->op_exec_duration = p->new_histogram({
        {"pine_operator_exec_duration_seconds", "Operator execution duration in seconds.", {"operator"}},
        {0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0}
    });
    em->op_skip_total = p->new_counter({"pine_operator_skip_total", "Total skipped operator executions.", {"operator"}});
    em->op_error_total = p->new_counter({"pine_operator_error_total", "Total failed operator executions.", {"operator"}});
    em->dag_exec_total = p->new_counter({"pine_dag_executions_total", "Total DAG executions.", {"status"}});
    em->dag_exec_duration = p->new_histogram({
        {"pine_dag_execution_duration_seconds", "DAG execution duration in seconds.", {}},
        {0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0}
    });
    em->dag_ops_executed = p->new_histogram({
        {"pine_dag_operators_executed", "Number of operators executed per DAG run.", {}},
        {1, 5, 10, 20, 50, 100, 200}
    });
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
    explicit PoolHolder(std::size_t n) : pool(n) {}
};

Engine::Engine(Config config) : Engine(std::move(config), EngineOptions{}) {}

Engine::~Engine() = default;
Engine::Engine(Engine&&) noexcept = default;
Engine& Engine::operator=(Engine&&) noexcept = default;

Engine::Engine(Config config, EngineOptions options) : config_(std::move(config)) {
    bool global_debug = options.debug.has_value() ? *options.debug : config_.debug;
    if (global_debug) {
        for (auto& [_, op] : config_.operators) op.debug = true;
    }
    log_prefix_ = options.log_prefix.has_value() ? *options.log_prefix : config_.log_prefix;
    peak_concurrency_ = std::make_unique<std::atomic<int64_t>>(0);
    metrics_provider_ = options.metrics_provider ? options.metrics_provider : metrics::nop_provider();
    // Engine-level worker pool reused across data_parallel shards. Sized to
    // hardware_concurrency (≥1). Avoids the per-request spawn/join cost
    // that becomes dominant under high QPS. P1-P1.
    {
        unsigned hw = std::thread::hardware_concurrency();
        if (hw == 0) hw = 4;
        shard_pool_ = std::make_unique<PoolHolder>(static_cast<std::size_t>(hw));
    }
    expanded_ = expand_operator_sequence_with_subflows(config_);
    graph_ = build_dag(config_, expanded_);
    engine_metrics_ = build_engine_metrics(metrics_provider_, expanded_.sequence);

    // Validate sources order matching pine-go validateSourcesOrder.
    // Sources references must exist and be declared before the current operator.
    std::set<std::string> seen_ops;
    for (const auto& op_name : expanded_.sequence) {
        const auto& op_cfg = config_.operators.at(op_name);
        for (const auto& src : op_cfg.sources) {
            if (!seen_ops.count(src)) {
                throw ValidationError("operator \"" + op_name + "\": sources references \"" + src + "\" which is declared after the current operator (forward reference)");
            }
        }
        seen_ops.insert(op_name);
    }

    // Instantiate and init one Operator per config operator.
    for (auto& [op_name, op_cfg] : config_.operators) {
        const auto* entry = registry_entry(op_cfg.type_name);
        if (!entry || !entry->factory) {
            throw RegistryError("operator \"" + op_name + "\": operator type not registered: \"" + op_cfg.type_name + "\"");
        }
        auto instance = entry->factory();
        instance->init(op_cfg);
        // Inject metrics provider for operators that opt-in. Mirrors
        // pine-go pine.go:170 — init first, then provider injection.
        if (auto* ma = dynamic_cast<MetricsAware*>(instance.get())) {
            ma->set_metrics_provider(metrics_provider_);
        }
        operators_.emplace(op_name, std::move(instance));
    }
}

Engine Engine::from_file(const std::string& path) { return Engine(load_config_from_file(path)); }
Engine Engine::from_file(const std::string& path, EngineOptions options) {
    return Engine(load_config_from_file(path), std::move(options));
}

namespace {

void dispatch_operator(const Frame& frame, const OperatorConfig& op,
                       const std::map<std::string, std::unique_ptr<Operator>>& operators,
                       OperatorOutput& out) {
    auto it = operators.find(op.name);
    if (it == operators.end() || !it->second) {
        throw RegistryError("operator \"" + op.name + "\": operator type not registered: \"" + op.type_name + "\"");
    }
    it->second->execute(frame, out);
}

void validate_item_inputs(const Frame& frame, const OperatorConfig& op) {
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        for (const auto& field : op.metadata.item_input) {
            if (op.item_defaults.count(field)) continue;
            JsonValue v = frame.item(i, field);
            if (v.is_null()) {
                throw ExecutionError(op.name, "required field \"" + field + "\" is nil on item[" + std::to_string(i) + "]");
            }
        }
    }
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
void dispatch_with_recovery(const Frame& frame, const OperatorConfig& op,
                            const std::map<std::string, std::unique_ptr<Operator>>& operators,
                            OperatorOutput& out) {
    try {
        dispatch_operator(frame, op, operators, out);
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
        throw PanicError(op.name, "unknown exception");
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
// reviewer-flagged silent-drop path in inc1 #4.
void merge_shard_output(OperatorOutput& dst, const OperatorOutput& src,
                        int offset, const std::string& op_name) {
    if (!src.added_items().empty() || src.has_item_order() ||
        !src.common_writes().empty()) {
        throw PanicError(op_name,
            "data_parallel shard emitted added_items, item_order, or common "
            "writes; only item_writes / removed_items / warnings are allowed "
            "(see parallel_execute preconditions)");
    }
    for (const auto& [idx, fields] : src.item_writes()) {
        for (const auto& [field, value] : fields) {
            dst.set_item(idx + offset, field, value);
        }
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
// pine-go gets for free through goroutines (P1-P1). When `pool` is null
// the legacy per-shard std::thread fallback runs — kept so unit tests and
// stand-alone callers that construct Frame directly still work.
void parallel_execute(const Frame& frame, const OperatorConfig& op,
                      const std::map<std::string, std::unique_ptr<Operator>>& operators,
                      OperatorOutput& out,
                      runtime::ThreadPool* pool = nullptr) {
    int total = static_cast<int>(frame.item_count());
    int n = op.data_parallel;
    if (n <= 1 || total == 0) {
        dispatch_with_recovery(frame, op, operators, out);
        return;
    }
    if (n > total) n = total;

    int base = total / n;
    int rem = total % n;

    // Materialize the source frame's common into a plain map for shard
    // construction. The original ColumnFrame's resources pointer is shared.
    std::map<std::string, JsonValue> common_snapshot;
    for (const auto& f : frame.common_fields()) {
        common_snapshot[f] = frame.common(f);
    }

    std::vector<std::unique_ptr<Frame>> shards;
    shards.reserve(static_cast<std::size_t>(n));
    std::vector<OperatorOutput> shard_outs(static_cast<std::size_t>(n));
    std::vector<int> offsets(static_cast<std::size_t>(n));
    int cursor = 0;
    for (int i = 0; i < n; ++i) {
        int size = base + (i < rem ? 1 : 0);
        std::vector<std::map<std::string, JsonValue>> shard_items;
        shard_items.reserve(static_cast<std::size_t>(size));
        for (int j = 0; j < size; ++j) {
            auto obj = frame.item_object(static_cast<std::size_t>(cursor + j));
            shard_items.emplace_back(obj.begin(), obj.end());
        }
        auto shard = std::make_unique<Frame>(common_snapshot, std::move(shard_items));
        shard->set_resources(frame.resources());
        shards.push_back(std::move(shard));
        offsets[static_cast<std::size_t>(i)] = cursor;
        cursor += size;
    }

    std::mutex err_mu;
    std::exception_ptr first_err;

    auto shard_body = [&](int i) {
        try {
            dispatch_with_recovery(*shards[static_cast<std::size_t>(i)], op, operators,
                                   shard_outs[static_cast<std::size_t>(i)]);
        } catch (...) {
            std::lock_guard<std::mutex> lk(err_mu);
            if (!first_err) first_err = std::current_exception();
        }
    };

    if (pool != nullptr) {
        std::vector<std::future<void>> futs;
        futs.reserve(static_cast<std::size_t>(n));
        for (int i = 0; i < n; ++i) {
            futs.push_back(pool->submit([&, i]() { shard_body(i); }));
        }
        for (auto& f : futs) f.wait();
    } else {
        std::vector<std::thread> threads;
        threads.reserve(static_cast<std::size_t>(n));
        for (int i = 0; i < n; ++i) {
            threads.emplace_back([&, i]() { shard_body(i); });
        }
        for (auto& t : threads) t.join();
    }

    if (first_err) std::rethrow_exception(first_err);

    for (int i = 0; i < n; ++i) {
        merge_shard_output(out, shard_outs[static_cast<std::size_t>(i)],
                           offsets[static_cast<std::size_t>(i)], op.name);
    }
}

// run_dag executes the DAG concurrently: each node runs on its own thread,
// waits on predecessor completion via shared_futures, and accesses Frame
// under a shared_mutex (shared lock for reads, unique lock for apply_output).
// On the first fatal exception, all unstarted nodes observe `cancelled` and
// bail out; the captured exception is rethrown by the caller.
// Mirrors pine-go internal/runtime/scheduler.go (per-node goroutines, done
// channels, fatalOnce + context cancel).
std::vector<OpTrace> run_dag(const Config& config,
                             const Graph& graph,
                             const std::map<std::string, std::unique_ptr<Operator>>& operators,
                             Frame& frame,
                             bool collect_traces,
                             std::atomic<int64_t>* peak_concurrency = nullptr,
                             Engine::EngineMetrics* em = nullptr,
                             runtime::ThreadPool* shard_pool = nullptr) {
    const std::size_t n = graph.nodes.size();

    if (em && em->scheduler_runs) em->scheduler_runs->inc();
    auto dag_start = std::chrono::steady_clock::now();
    std::atomic<int64_t> ops_executed{0};

    // NodeSignal replaces std::promise/shared_future so that a fail() can
    // interrupt waits on still-pending predecessors through a stop_token,
    // rather than the old "wait until predecessor self-terminates" pattern
    // which forced every sibling to block until its own predecessors
    // finished (inc1 #5). condition_variable_any::wait(lock, stop_token,
    // pred) is C++20 P0660 — it returns immediately when stop is requested.
    struct NodeSignal {
        std::mutex mu;
        std::condition_variable_any cv;
        bool done = false;
    };
    std::vector<std::unique_ptr<NodeSignal>> signals;
    signals.reserve(n);
    for (std::size_t i = 0; i < n; ++i) {
        signals.emplace_back(std::make_unique<NodeSignal>());
    }

    std::vector<OpTrace> traces;
    if (collect_traces) traces.assign(n, OpTrace{});

    // P1-P3: Frame concurrency is managed by ColumnFrame's internal
    // shared_mutex (column_frame.hpp:79). The DAG topology guarantees
    // predecessor-successor ordering through NodeSignal, and parallel_execute
    // shards copy items out before mutating, so siblings on the same DAG
    // level only ever issue concurrent reads. We removed the redundant
    // engine-level frame_mu — the inner lock is the source of truth.
    std::mutex fatal_mu;
    std::exception_ptr fatal_err;
    std::atomic<int64_t> active_ops{0};

    // stop_source broadcasts cancellation to every node waiting on a
    // predecessor via cv.wait(lock, stop_token, pred). Replacing the
    // old `std::atomic<bool> cancelled` with stop_token lets the cv
    // implementation interrupt the wait directly — no polling, no
    // sleep_for. P1-P2.
    std::stop_source cancel_source;
    auto stop_token = cancel_source.get_token();

    auto fail = [&](std::exception_ptr e) {
        std::lock_guard<std::mutex> lk(fatal_mu);
        if (!fatal_err) {
            fatal_err = e;
            cancel_source.request_stop();
        }
    };

    auto mark_done = [&](std::size_t idx) {
        auto& s = *signals[idx];
        {
            std::lock_guard<std::mutex> lk(s.mu);
            s.done = true;
        }
        s.cv.notify_all();
    };

    // CAS-update the cumulative peak to at least n_current.
    // Mirrors pine-go internal/runtime/stats.go Stats.RecordConcurrency.
    auto record_peak = [&](int64_t n_current) {
        if (!peak_concurrency) return;
        for (;;) {
            int64_t cur = peak_concurrency->load(std::memory_order_relaxed);
            if (n_current <= cur) return;
            if (peak_concurrency->compare_exchange_weak(cur, n_current,
                                                       std::memory_order_relaxed)) {
                return;
            }
        }
    };

    std::vector<std::thread> threads;
    threads.reserve(n);
    for (std::size_t i = 0; i < n; ++i) {
        threads.emplace_back([&, i]() {
            // RAII: always notify successors, even on early return / exception.
            struct Notifier {
                std::function<void()> fn;
                ~Notifier() { try { fn(); } catch (...) {} }
            } notifier{[&, i] { mark_done(i); }};
            (void)notifier;

            const auto& node = graph.nodes[i];
            const auto& op = config.operators.at(node.name);

            // Wait on every predecessor's done flag; cv.wait with stop_token
            // returns false when fail() requests stop, letting us bail out
            // without serializing through still-running siblings.
            for (int pred : node.preds) {
                auto& s = *signals[static_cast<std::size_t>(pred)];
                std::unique_lock<std::mutex> lk(s.mu);
                bool got_pred = s.cv.wait(lk, stop_token, [&] { return s.done; });
                if (!got_pred) return;  // stop requested
            }
            if (stop_token.stop_requested()) return;

            // Track active concurrency (mirrors pine-go scheduler activeOps + RecordConcurrency).
            // P1-E1: Gauge must be updated on both increment AND decrement.
            // The earlier guard only fetch_sub'd the atomic without touching
            // the Prometheus handle, leaving pine_operator_active monotonic-
            // increasing and meaningless for monitoring.
            struct ActiveGuard {
                std::atomic<int64_t>& counter;
                metrics::Gauge* gauge;
                ~ActiveGuard() {
                    int64_t after = counter.fetch_sub(1, std::memory_order_relaxed) - 1;
                    if (gauge) gauge->set(static_cast<double>(after));
                }
            };
            int64_t cur_active = active_ops.fetch_add(1, std::memory_order_relaxed) + 1;
            ActiveGuard active_guard{active_ops, em ? em->active_ops : nullptr};
            record_peak(cur_active);
            if (em && em->active_ops) em->active_ops->set(static_cast<double>(cur_active));

            OpTrace trace;
            if (collect_traces) {
                trace.name = op.name;
                trace.start_time_us = now_us();
            }
            auto start = std::chrono::steady_clock::now();

            try {
                bool skip = should_skip(frame, op);
                if (skip) {
                    if (em && em->op_skip_total) em->op_skip_total->with({op.name})->inc();
                    if (collect_traces) {
                        auto end = std::chrono::steady_clock::now();
                        trace.duration_us = std::chrono::duration_cast<std::chrono::microseconds>(end - start).count();
                        trace.skipped = true;
                        traces[i] = std::move(trace);
                    }
                    return;
                }

                OperatorOutput out;
                validate_item_inputs(frame, op);
                if (collect_traces && op.debug) {
                    trace.input_snapshot = snapshot_input(frame, op);
                    trace.has_input_snapshot = true;
                }
                parallel_execute(frame, op, operators, out, shard_pool);
                if (collect_traces && op.debug) {
                    trace.output_snapshot = snapshot_output(out);
                    trace.has_output_snapshot = true;
                }
                frame.apply_output(out, op.name, op.operator_type == "recall");
                auto end = std::chrono::steady_clock::now();
                auto dur_ns = std::chrono::duration_cast<std::chrono::nanoseconds>(end - start);
                if (em) {
                    if (em->op_exec_total) em->op_exec_total->with({op.name})->inc();
                    if (em->op_exec_duration) em->op_exec_duration->with({op.name})->observe(metrics::duration_seconds(dur_ns));
                }
                ops_executed.fetch_add(1, std::memory_order_relaxed);
                if (collect_traces) {
                    trace.duration_us = std::chrono::duration_cast<std::chrono::microseconds>(end - start).count();
                    traces[i] = std::move(trace);
                }
            } catch (...) {
                if (em && em->op_error_total) em->op_error_total->with({op.name})->inc();
                fail(std::current_exception());
            }
        });
    }

    for (auto& t : threads) t.join();

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

    if (fatal_err) std::rethrow_exception(fatal_err);

    return traces;
}

}  // namespace

Result Engine::execute(const Request& request) const {
    static const std::map<std::string, JsonValue> empty_resources;
    return execute(request, empty_resources);
}

Result Engine::execute(const Request& request, const std::map<std::string, JsonValue>& resources) const {
    validate_request(request, config_.flow_contract);
    Frame frame(request.common, request.items);
    frame.set_resources(&resources);
    run_dag(config_, graph_, operators_, frame, /*collect_traces=*/false, peak_concurrency_.get(), engine_metrics_.get(),
            shard_pool_ ? &shard_pool_->pool : nullptr);
    return project_result(frame, config_.flow_contract);
}

TracedResult Engine::execute_traced(const Request& request, const std::map<std::string, JsonValue>& resources) const {
    TracedResult traced;
    execute_traced_into(request, resources, &traced);
    return traced;
}

void Engine::execute_traced_into(const Request& request,
                                  const std::map<std::string, JsonValue>& resources,
                                  TracedResult* out) const {
    validate_request(request, config_.flow_contract);
    Frame frame(request.common, request.items);
    frame.set_resources(&resources);
    std::exception_ptr run_err = nullptr;
    try {
        out->trace = run_dag(config_, graph_, operators_, frame, /*collect_traces=*/true, peak_concurrency_.get(), engine_metrics_.get(),
                             shard_pool_ ? &shard_pool_->pool : nullptr);
    } catch (...) {
        run_err = std::current_exception();
    }
    // Match Go: even on execution failure we project the partial Result and take warnings
    out->result = project_result(frame, config_.flow_contract);
    out->warnings = frame.take_warnings();
    if (run_err) std::rethrow_exception(run_err);
}

std::string Engine::render_dag(const std::string& format, int collapse) const {
    if (format == "dot") return collapse > 0 ? render_collapsed_dot(graph_, collapse) : render_dot(graph_);
    if (format == "mermaid") return collapse > 0 ? render_collapsed_mermaid(graph_, collapse) : render_mermaid(graph_);
    throw ValidationError("unsupported DAG format \"" + format + "\" (use \"dot\" or \"mermaid\")");
}

int64_t Engine::peak_concurrency() const {
    return peak_concurrency_ ? peak_concurrency_->load(std::memory_order_relaxed) : 0;
}

std::map<std::string, std::map<std::string, int64_t>> Engine::operator_custom_stats() const {
    std::map<std::string, std::map<std::string, int64_t>> result;
    for (const auto& cop : expanded_.sequence) {
        auto it = operators_.find(cop); // cop is std::string
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

}  // namespace pine
