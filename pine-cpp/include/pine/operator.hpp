#pragma once
#include "pine/column_frame.hpp"
#include "pine/frame.hpp"
#include "pine/metrics.hpp"
#include "pine/pine.hpp"

#include <functional>
#include <map>
#include <memory>
#include <string>
#include <vector>

namespace pine {

// Frame is the polymorphic DataFrame base. ColumnFrame is the default
// implementation; RowFrame ships in src/dataframe/row_frame.cpp.
// (Was previously `using Frame = ColumnFrame;` aliased to the
// single MVP impl.)

enum class OpType { Recall, Transform, Filter, Merge, Reorder, Observe };
const char* op_type_to_string(OpType t);         // "recall" / "transform" / ...
const char* op_type_to_schema_string(OpType t);  // "Recall" / "Transform" / ... (首字母大写,用于 schema JSON)

// --- Marker 空基类 (Commit B 完成后会被真正的接口取代) ---
struct ConsumesRowSet {};
struct MutatesRowSet {};
struct AdditiveWritesRowSet {};
struct ConcurrentSafe {};

// MetricsAware mirrors pine-go's types.MetricsAware optional interface.
// Operators that record metrics to an external provider implement this;
// Engine injects the configured metrics::Provider after init() but before
// the first execute() call (matches pine-go pine.go:170 ordering).
class MetricsAware {
 public:
  virtual ~MetricsAware() = default;
  virtual void set_metrics_provider(metrics::Provider* provider) = 0;
};

// ResourceAware mirrors pine-go / pine-java's ResourceAware optional
// interface. Operators that borrow handle-typed resources (e.g. a Redis
// connection pool) by name implement this; Engine injects the configured
// ResourceProvider after init() but before the first execute() call, in the
// same place and ordering as MetricsAware. The injected pointer may be null
// (no resource provider configured), in which case the operator degrades at
// execute time. The borrowed handle must not be cached across execute() calls.
class ResourceAware {
 public:
  virtual ~ResourceAware() = default;
  virtual void set_resource_provider(const ResourceProvider* provider) = 0;
};

// StatsProvider mirrors pine-go's types.StatsProvider optional interface.
// Operators that expose internal counters/gauges for the /stats endpoint
// implement this. The Engine polls OperatorStats() during stats collection.
class StatsProvider {
 public:
  virtual ~StatsProvider() = default;
  virtual std::map<std::string, int64_t> operator_stats() const = 0;
};

// Closer mirrors pine-go's types.Closer / pine-java's Closer optional
// interface. Operators holding external resources (e.g. a pool of Lua
// interpreter states) implement it; Engine::close() calls close() on every
// operator that does when the engine is retired (config hot-reload or
// shutdown). C++ already releases these resources deterministically via RAII
// when the Engine is destroyed, so close() is not required for correctness
// here — it exists for cross-runtime semantic parity and to make the release
// point explicit and observable. Implementations must be idempotent and leave
// the operator safe to destroy afterwards.
class Closer {
 public:
  virtual ~Closer() = default;
  virtual void close() = 0;
};

// Note: Operator base class is declared in pine/pine.hpp (as class Operator)
// with execute(const OperatorInput&, OperatorOutput&). Operators receive a
// pre-projected OperatorInput snapshot with defaults applied.

// --- OperatorSchema ---

// MetadataDoc holds the typical CommonInput / CommonOutput / ItemInput /
// ItemOutput contract for an operator, surfaced in the generated markdown's
// "Metadata Contract" section. Empty fields render as "-". Cross-engine
// codegen contract: pine-go parses these from source comments, pine-java
// from javadoc; pine-cpp has no source parser, so operator authors set them
// inline alongside the rest of the schema.
struct MetadataDoc {
  std::string common_input;
  std::string common_output;
  std::string item_input;
  std::string item_output;
};

struct OperatorSchema {
  std::string name;
  OpType type;
  std::string description;
  std::map<std::string, ParamSchema> params;
  MetadataDoc metadata;  // Optional; empty fields render as "-" in generated markdown.
};

using OperatorFactory = std::function<std::unique_ptr<Operator>()>;

// OperatorTraits resolves marker-interface presence at compile time via
// constexpr std::is_base_of_v, so the factory is invoked exactly once per
// Engine instantiation.
template <typename T>
struct OperatorTraits {
  static constexpr bool consumes_row_set = std::is_base_of_v<ConsumesRowSet, T>;
  static constexpr bool mutates_row_set = std::is_base_of_v<MutatesRowSet, T>;
  static constexpr bool additive_writes_row_set = std::is_base_of_v<AdditiveWritesRowSet, T>;
  static constexpr bool concurrent_safe = std::is_base_of_v<ConcurrentSafe, T>;
};

struct OperatorEntry {
  OperatorSchema schema;
  OperatorFactory factory;
  bool consumes_row_set = false;
  bool mutates_row_set = false;
  bool additive_writes_row_set = false;
  bool concurrent_safe = false;
};

// Register an operator with pre-computed marker bits. Used by
// register_operator_typed<T>; resolves traits at compile time, so operators
// with heavyweight constructors pay only the per-Engine instantiation cost.
//
// Throws RegistryError on: empty name, empty description, null factory,
// duplicate name.
void register_operator_with_traits(OperatorSchema schema, OperatorFactory factory, bool consumes_row_set,
                                   bool mutates_row_set, bool additive_writes_row_set, bool concurrent_safe);

// Templated entry: derives marker bits from OperatorTraits<T> at compile
// time, so the registry never needs a dynamic_cast probe.
template <typename T>
void register_operator_typed(OperatorSchema schema) {
  static_assert(std::is_base_of_v<Operator, T>,
                "register_operator_typed<T>: T must derive from pine::Operator");
  register_operator_with_traits(
      std::move(schema), [] { return std::make_unique<T>(); }, OperatorTraits<T>::consumes_row_set,
      OperatorTraits<T>::mutates_row_set, OperatorTraits<T>::additive_writes_row_set,
      OperatorTraits<T>::concurrent_safe);
}

// Lookup an operator entry by type name; returns nullptr if not registered.
const OperatorEntry* registry_entry(const std::string& type_name);

// List all registered operator names (sorted).
std::vector<std::string> registered_operator_names();

// --- Macro helpers ---
#define PINE_DETAIL_CONCAT_INNER(a, b) a##b
#define PINE_DETAIL_CONCAT(a, b)       PINE_DETAIL_CONCAT_INNER(a, b)

// PINE_REGISTER_OPERATOR_T registers an operator at static initialization
// time. It accepts the operator type directly so marker bits resolve at
// compile time via OperatorTraits<T> — no factory probe, no dynamic_cast.
//
// Example:
//   static const pine::OperatorSchema k_my_op_schema{ ... };
//   PINE_REGISTER_OPERATOR_T(MyOp, k_my_op_schema)
#define PINE_REGISTER_OPERATOR_T(T, SCHEMA)                       \
  namespace {                                                     \
  const bool PINE_DETAIL_CONCAT(_pine_reg_t_, __COUNTER__) = [] { \
    ::pine::register_operator_typed<T>((SCHEMA));                 \
    return true;                                                  \
  }();                                                            \
  }

}  // namespace pine
