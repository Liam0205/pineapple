#pragma once

#include "pine/arena.hpp"
#include "pine/flat_map.hpp"
#include "pine/metrics.hpp"

#include <atomic>
#include <exception>
#include <map>
#include <memory>
#include <optional>
#include <set>
#include <stdexcept>
#include <stop_token>
#include <string>
#include <unordered_map>
#include <utility>
#include <variant>
#include <vector>

namespace pine {

// Pineapple engine version. Single source of truth for all C++ code —
// mirrors pine-go's `const Version` in pine-go/version.go. Keep in sync
// with pine-go and the _PINEAPPLE_VERSION field embedded in compiled
// configs.
inline constexpr const char* kVersion = "0.9.8";

class Error : public std::runtime_error {
 public:
  using std::runtime_error::runtime_error;
};

// ConfigError, ValidationError, RegistryError auto-prefix their what() output
// with `pine: <category> error: ...` to match pine-go's Error() format
// (types/errors.go ConfigError/ValidationError/RegistryError). Throw sites
// should pass the bare message body; the prefix is added once at construction.
class ConfigError : public Error {
 public:
  explicit ConfigError(const std::string& msg) : Error("pine: config error: " + msg) {
  }
  explicit ConfigError(const char* msg) : Error(std::string("pine: config error: ") + msg) {
  }
};

class ValidationError : public Error {
 public:
  explicit ValidationError(const std::string& msg) : Error("pine: validation error: " + msg) {
  }
  explicit ValidationError(const char* msg) : Error(std::string("pine: validation error: ") + msg) {
  }
};

// RegistryError supports both `pine: registry error: <msg>` (legacy 1-arg
// throw sites where the operator name is already embedded in the message)
// and `pine: registry error [<op>]: <msg>` (matches pine-go RegistryError
// with explicit Operator field).
class RegistryError : public Error {
 public:
  explicit RegistryError(const std::string& msg) : Error("pine: registry error: " + msg) {
  }
  explicit RegistryError(const char* msg) : Error(std::string("pine: registry error: ") + msg) {
  }
  RegistryError(const std::string& operator_name, const std::string& msg)
      : Error("pine: registry error [" + operator_name + "]: " + msg) {
  }
};

// ExecutionError carries the operator name and an inner error message and
// formats like pine-go: `pine: execution error in operator "X": <inner>`.
//
// Two construction forms:
//   ExecutionError(operator_name, inner_msg)
//     The canonical form. Builds the full `pine: execution error in
//     operator "X": <inner>` what() string at construction time.
//
//   ExecutionError(msg)
//     The legacy single-string form, used by operator-level throw sites
//     that don't know their own operator name. `operator_name()` returns
//     empty; `inner()` returns the message verbatim. The engine layer
//     (`dispatch_with_recovery`, see runtime/engine.cpp) catches this
//     form and re-wraps it via `std::throw_with_nested(ExecutionError(
//     op.name, e.inner()))` so the eventual what() string still matches
//     pine-go byte-for-byte. **In short: operator code may throw with
//     either form; the byte-exact prefix is applied centrally by the
//     scheduler, not by every throw site.**
//
// Also inherits std::nested_exception so that when thrown via
// std::throw_with_nested the original in-flight exception is preserved as a
// nested cause. Use pine::error_as<T>() (include/pine/error_chain.hpp) to
// walk the cause chain, mirroring Go's errors.As / Java's Throwable.getCause().
class ExecutionError : public Error, public std::nested_exception {
 public:
  explicit ExecutionError(std::string msg) : Error(msg), inner_(std::move(msg)) {
  }
  ExecutionError(std::string operator_name, std::string inner)
      : Error(format_msg(operator_name, inner)),
        operator_(std::move(operator_name)),
        inner_(std::move(inner)) {
  }
  const std::string& operator_name() const {
    return operator_;
  }
  const std::string& inner() const {
    return inner_;
  }

 private:
  static std::string format_msg(const std::string& op, const std::string& inner) {
    return "pine: execution error in operator \"" + op + "\": " + inner;
  }
  std::string operator_;
  std::string inner_;
};

// PanicError wraps an unexpected (non-pine::Error) exception thrown from an
// operator. Mirrors pine-go's types.PanicError. Inherits std::nested_exception
// so the recovered std::exception is preserved as a nested cause when thrown
// via std::throw_with_nested.
//
// detailed_error() returns the message plus a stack trace captured
// at construction time, mirroring pine-go's PanicError.DetailedError().
// stack() exposes the raw frames as a string. The capture is best-effort —
// C++ has no goroutine concept, so the trace is the constructing thread's
// frames only (use std::stacktrace::current via std::stacktrace_entry).
class PanicError : public Error, public std::nested_exception {
 public:
  PanicError(std::string operator_name, std::string value);
  const std::string& operator_name() const {
    return operator_;
  }
  const std::string& value() const {
    return value_;
  }
  const std::string& stack() const {
    return stack_;
  }
  std::string detailed_error() const;

 private:
  static std::string format_msg(const std::string& op, const std::string& v) {
    return "pine: panic in operator \"" + op + "\": " + v;
  }
  std::string operator_;
  std::string value_;
  std::string stack_;  // empty when std::stacktrace unavailable at link time
};

class Variant;
using Object = FlatMap<Variant>;
using Array = std::vector<Variant>;

class Variant {
 public:
  using object_t = Object;
  using array_t = Array;
  using value_t = std::variant<std::nullptr_t, bool, double, std::string, array_t, object_t>;

  Variant();
  Variant(std::nullptr_t);
  Variant(bool value);
  Variant(double value);
  Variant(int value);
  Variant(std::string value);
  Variant(const char* value);
  Variant(array_t value);
  Variant(object_t value);

  bool is_null() const;
  bool is_bool() const;
  bool is_number() const;
  bool is_string() const;
  bool is_array() const;
  bool is_object() const;

  bool as_bool() const;
  double as_number() const;
  const std::string& as_string() const;
  const array_t& as_array() const;
  const object_t& as_object() const;
  array_t& as_array();
  object_t& as_object();

  bool truthy() const;

  const Variant* find(const std::string& key) const;
  Variant* find(const std::string& key);

 private:
  value_t value_;
};

Variant parse_json(const std::string& text);
std::string dump_json(const Variant& value, int indent = 2);

struct Metadata {
  std::vector<std::string> common_input;
  std::vector<std::string> common_output;
  std::vector<std::string> item_input;
  std::vector<std::string> item_output;
};

struct OperatorConfig {
  std::string name;
  std::string type_name;
  Metadata metadata;
  std::vector<std::string> skip;
  bool recall = false;
  bool consumes_row_set = false;
  bool mutates_row_set = false;
  bool additive_writes_row_set = false;
  bool concurrent_safe = false;
  std::optional<bool> debug;
  bool for_branch_control = false;
  std::map<std::string, Variant> common_defaults;
  std::map<std::string, Variant> item_defaults;
  std::vector<std::string> strict_common;
  std::vector<std::string> strict_item;
  std::vector<std::string> sources;
  Variant params;
  std::string operator_type;
  int data_parallel = 0;
};

// InputFieldSpec describes which fields are strict (must be non-nil) vs
// defaulted (nil → use default) for an operator. Computed once at config
// load time from metadata + defaults maps.
struct DefaultedField {
  std::string name;
  Variant default_value;
};

struct InputFieldSpec {
  std::vector<std::string> strict_common;
  std::vector<DefaultedField> defaulted_common;
  std::vector<std::string> nullable_common;
  std::vector<std::string> strict_item;
  std::vector<DefaultedField> defaulted_item;
  std::vector<std::string> nullable_item;
};

// compute_input_field_spec derives the InputFieldSpec from an OperatorConfig.
InputFieldSpec compute_input_field_spec(const OperatorConfig& config);

struct FlowContract {
  std::vector<std::string> common_input;
  std::vector<std::string> item_input;
  std::vector<std::string> common_output;
  std::vector<std::string> item_output;
};

// ResourceEntry mirrors pine-go's resource.resourceConfig: a single entry in
// the root `resource_config` map. C++ does not yet have a Manager runtime
// subsystem (callers still inject resources via Frame::set_resources), so
// these fields are parsed and stored for downstream tooling / future wiring
// without producing fetchers at config-load time.
struct ResourceEntry {
  std::string type;
  int interval = 0;  // seconds
  Variant params;    // arbitrary object passed to the fetcher factory
};

struct Config {
  std::map<std::string, OperatorConfig> operators;
  std::map<std::string, std::vector<std::string>> pipeline_map;
  std::map<std::string, std::vector<std::string>> pipeline_group;
  FlowContract flow_contract;
  std::string storage_mode = "row";
  bool debug = false;
  std::string log_prefix;
  // Metadata fields written by codegen at config-build time. Surfaced
  // verbatim so downstream tooling can read them; the engine itself does
  // not act on them. Mirrors pine-go RootConfig._PINEAPPLE_VERSION /
  // _PINEAPPLE_CREATE_TIME.
  std::string pineapple_version;
  std::string pineapple_create_time;
  // Optional resource_config block. Empty when omitted.
  std::map<std::string, ResourceEntry> resource_config;
};

struct ExpandedSequence {
  std::vector<std::string> sequence;
  std::map<std::string, std::string> op_to_subflow;
};

Config load_config_from_file(const std::string& path);
Config load_config_from_json(const std::string& text);
ExpandedSequence expand_operator_sequence_with_subflows(const Config& config);

// --- Operator Registry (equivalent to Go's pine.Register + marker interfaces) ---

struct ParamSchema {
  std::string type;
  bool required = false;
  Variant default_value;  // null means no default
  std::string description;
};

void apply_registry_traits(Config& config);
std::string export_schema_json();

struct Node {
  std::string name;
  std::string subflow;
  std::vector<int> preds;
  std::vector<int> succs;
  const OperatorConfig* config = nullptr;
};

struct Graph {
  std::vector<Node> nodes;
  std::map<std::string, int> name_to_index;
};

Graph build_dag(const Config& config, const ExpandedSequence& expanded);
std::string render_dot(const Graph& graph);
std::string render_mermaid(const Graph& graph);
std::string render_collapsed_dot(const Graph& graph, int level);
std::string render_collapsed_mermaid(const Graph& graph, int level);

struct Request {
  Variant::object_t common;
  std::vector<Variant::object_t> items;
};

struct Result {
  Variant::object_t common;
  std::vector<Variant::object_t> items;
};

// OperatorOutput collects writes from an operator, applied to the DataFrame by
// the engine. Mirrors pine-go's types.OperatorOutput.
//
// Sequence enforced by Frame::apply_output: common writes → item writes →
// removals → reorder → additions.
class OperatorOutput {
 public:
  OperatorOutput() = default;

  // ItemWrite is a single (index, field, value) log entry. set_item()
  // appends; apply_output replays in order (last write wins per cell).
  // Replaces the old nested map<int, map<string, Variant>> which paid
  // two tree-node allocations per write.
  struct ItemWrite {
    int index;
    std::string field;
    Variant value;
  };

  void set_common(const std::string& field, Variant value);
  void set_item(int index, const std::string& field, Variant value);
  void add_item(Variant::object_t fields);
  void remove_item(int index);
  void set_item_order(std::vector<int> order);
  void set_warning(std::string msg);  // first warning wins

  const Variant::object_t& common_writes() const {
    return common_writes_;
  }
  const std::vector<ItemWrite>& item_writes() const {
    return item_writes_;
  }
  const std::vector<Variant::object_t>& added_items() const {
    return added_items_;
  }
  const std::set<int>& removed_items() const {
    return removed_items_;
  }
  const std::vector<int>& item_order() const {
    return item_order_;
  }
  bool has_item_order() const {
    return has_item_order_;
  }
  const std::string& warning() const {
    return warning_;
  }
  bool has_warning() const {
    return has_warning_;
  }

 private:
  Variant::object_t common_writes_;
  std::vector<ItemWrite> item_writes_;
  std::vector<Variant::object_t> added_items_;
  std::set<int> removed_items_;
  std::vector<int> item_order_;
  bool has_item_order_ = false;
  std::string warning_;
  bool has_warning_ = false;
};

struct OpTrace {
  std::string name;
  int64_t start_time_us = 0;  // microseconds since unix epoch (system_clock)
  int64_t duration_us = 0;
  bool skipped = false;
  bool has_input_snapshot = false;
  Variant input_snapshot;  // object {common?, items?}
  bool has_output_snapshot = false;
  Variant output_snapshot;  // object {common_writes?, item_writes?, added_items?, removed_items?}
};

struct TracedResult {
  Result result;
  std::vector<OpTrace> trace;
  std::vector<std::string> warnings;
};

// ResourceProvider is the read-only view an operator uses to borrow a
// handle-typed resource (e.g. a Redis connection pool) by name. Mirrors
// pine-go / pine-java's ResourceProvider. resource::Manager implements it.
// The returned shared_ptr is a process-internal handle, type-erased as
// shared_ptr<void>; the caller static_pointer_casts it to the concrete type
// the matching fetcher stored. Returns nullptr when the resource is absent,
// not yet loaded, or not handle-typed — in which case the caller degrades.
//
// The borrowed handle must only be used within a single execute() call and
// must not be cached across calls: teardown safety relies on the engine's
// retirement lock ordering, which only guards in-flight executes.
class ResourceProvider {
 public:
  virtual ~ResourceProvider() = default;
  virtual std::shared_ptr<void> borrow(const std::string& name) const = 0;
};

struct EngineOptions {
  // When set, forces debug snapshot collection on/off, overriding Config.debug
  // and any per-operator debug flag in JSON. Mirrors Go's pine.WithDebug.
  std::optional<bool> debug;
  // When set, overrides root-level log_prefix from JSON config. Mirrors Go's
  // pine.WithLogPrefix. When both are empty/unset, no prefix is applied.
  std::optional<std::string> log_prefix;
  // Optional metrics provider. Defaults to metrics::nop_provider().
  // Mirrors Go's pine.WithMetrics.
  metrics::Provider* metrics_provider = nullptr;
  // Optional resource provider, used by ResourceAware operators to borrow
  // handle-typed resources (e.g. connection pools) by name. Defaults to null,
  // in which case those operators degrade at execute time. Mirrors pine-go /
  // pine-java's ResourceProvider injection.
  const ResourceProvider* resource_provider = nullptr;
  // DAG scheduler thread pool size. Default: nproc * 4.
  std::optional<std::size_t> dag_pool_size;
  // data_parallel shard thread pool size. Default: nproc * 2.
  std::optional<std::size_t> shard_pool_size;
};

// Forward declaration: ColumnFrame is defined in pine/column_frame.hpp.
// Operator::execute takes Frame (= ColumnFrame) by const ref.
class ColumnFrame;

// Forward declaration: Frame is defined in frame.hpp / column_frame.hpp.
class Frame;

// Forward declaration: OperatorInput is defined in operator_input.hpp.
class OperatorInput;

// Operator base class (mirrors pine-go's Operator interface).
// Concrete operators subclass this. Placed here so that Engine's
// unique_ptr<Operator> member has a complete type in all TUs that
// include pine.hpp.
class Operator {
 public:
  virtual ~Operator() = default;
  virtual void init(const OperatorConfig& config) {
    (void)config;
  }
  virtual void execute(const OperatorInput& input, OperatorOutput& out) = 0;
};

class Engine {
 public:
  explicit Engine(Config config);
  Engine(Config config, EngineOptions options);
  ~Engine();
  Engine(Engine&&) noexcept;
  Engine& operator=(Engine&&) noexcept;
  Engine(const Engine&) = delete;
  Engine& operator=(const Engine&) = delete;
  static Engine from_file(const std::string& path);
  static Engine from_file(const std::string& path, EngineOptions options);

  Result execute(const Request& request) const;
  Result execute(const Request& request, const std::map<std::string, Variant>& resources) const;
  // Cancellable variant: external_cancel.request_stop() (typically called
  // from a different thread when the client disconnects) interrupts any
  // cv.wait inside the DAG scheduler and aborts the run. Mirrors pine-go
  // Execute(ctx, req) where ctx.Done() is watched at every wait.
  Result execute(const Request& request, const std::map<std::string, Variant>& resources,
                 std::stop_token external_cancel) const;
  TracedResult execute_traced(const Request& request, const std::map<std::string, Variant>& resources) const;
  // Variant of execute_traced that writes the partial result/trace/warnings
  // into *out before re-throwing any execution error. Mirrors pine-go's
  // (*Result, error) return contract where partial results survive errors.
  void execute_traced_into(const Request& request, const std::map<std::string, Variant>& resources,
                           TracedResult* out) const;
  void execute_traced_into(const Request& request, const std::map<std::string, Variant>& resources,
                           TracedResult* out, std::stop_token external_cancel) const;
  std::string render_dag(const std::string& format, int collapse = 0) const;

  const ExpandedSequence& expanded() const {
    return expanded_;
  }
  const std::string& log_prefix() const {
    return log_prefix_;
  }
  // Cumulative peak DAG-node concurrency observed across all execute calls.
  // Mirrors pine-go internal/runtime/stats.go peakConcurrency.
  int64_t peak_concurrency() const;
  // Returns the configured metrics provider (never nullptr; nop by default).
  metrics::Provider* metrics_provider() const {
    return metrics_provider_;
  }

  // OperatorCustomStats collects custom statistics from operators that implement
  // StatsProvider. Returns an empty map when no operator reports custom stats.
  std::map<std::string, std::map<std::string, int64_t>> operator_custom_stats() const;

  // close tears down every operator that implements Closer. It is called when
  // the engine is retired — during a config hot-reload (on the swapped-out
  // engine) or on shutdown — mirroring pine-go's Engine.Close and pine-java's
  // Engine.close. C++ already releases operator resources deterministically via
  // RAII when the Engine is destroyed, so close() is not required for
  // correctness; it exists for cross-runtime semantic parity and to make the
  // release point explicit. Idempotent and safe to call before destruction.
  void close();

  // Pre-created scheduler/operator metrics. Public so the scheduler in
  // engine.cpp can record observations; not part of the stable public API.
  struct EngineMetrics;

 private:
  Config config_;
  ExpandedSequence expanded_;
  Graph graph_;
  std::map<std::string, std::unique_ptr<Operator>> operators_;
  std::map<std::string, InputFieldSpec> input_specs_;
  std::string log_prefix_;
  std::unique_ptr<std::atomic<int64_t>> peak_concurrency_;
  metrics::Provider* metrics_provider_ = nullptr;
  const ResourceProvider* resource_provider_ = nullptr;
  std::unique_ptr<EngineMetrics> engine_metrics_;
  struct PoolHolder;
  std::unique_ptr<PoolHolder> dag_pool_;
  std::unique_ptr<PoolHolder> shard_pool_;
};

Request load_request_from_file(const std::string& path);
std::string result_to_json(const Result& result);

}  // namespace pine
