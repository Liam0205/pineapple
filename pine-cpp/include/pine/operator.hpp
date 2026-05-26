#pragma once
#include "pine/pine.hpp"
#include "pine/column_frame.hpp"
#include <functional>
#include <map>
#include <memory>
#include <string>
#include <vector>

namespace pine {

// Frame is an alias for ColumnFrame, used throughout operator implementations.
using Frame = ColumnFrame;

enum class OpType { Recall, Transform, Filter, Merge, Reorder, Observe };
const char* op_type_to_string(OpType t);         // "recall" / "transform" / ...
const char* op_type_to_schema_string(OpType t);  // "Recall" / "Transform" / ... (首字母大写,用于 schema JSON)

// --- Marker 空基类 (Commit B 完成后会被真正的接口取代) ---
struct ConsumesRowSet {};
struct MutatesRowSet {};
struct AdditiveWritesRowSet {};
struct ConcurrentSafe {};

// Note: Operator base class is declared in pine/pine.hpp (as class Operator)
// with execute(const ColumnFrame&, OperatorOutput&). Frame = ColumnFrame, so
// overrides here use Frame directly for readability.

// --- OperatorSchema ---
struct OperatorSchema {
    std::string name;
    OpType type;
    std::string description;
    std::map<std::string, ParamSchema> params;
};

using OperatorFactory = std::function<std::unique_ptr<Operator>()>;

struct OperatorEntry {
    OperatorSchema schema;
    OperatorFactory factory;
    bool consumes_row_set = false;
    bool mutates_row_set = false;
    bool additive_writes_row_set = false;
    bool concurrent_safe = false;
};

// Register an operator into the global registry. Mirrors pine-go's pine.Register.
// Thread-safe; callable at any time, including before Engine construction.
//
// Throws RegistryError on:
//   - empty name
//   - empty description
//   - null factory
//   - factory() returns nullptr
//   - duplicate name
void register_operator(OperatorSchema schema, OperatorFactory factory);

// Lookup an operator entry by type name; returns nullptr if not registered.
const OperatorEntry* registry_entry(const std::string& type_name);

// List all registered operator names (sorted).
std::vector<std::string> registered_operator_names();

// PINE_REGISTER_OPERATOR registers an operator at static initialization time.
// Place at namespace scope in the operator's translation unit.
//
// IMPORTANT: Due to C preprocessor comma handling, the SCHEMA argument must be
// a named variable (not an inline brace-initializer), and the FACTORY argument
// must be wrapped in parentheses to protect template parameter commas.
//
// Example:
//   static const pine::OperatorSchema k_my_op_schema{ ... };
//   PINE_REGISTER_OPERATOR(k_my_op_schema,
//       ([] { return std::make_unique<MyOp>(); }))
#define PINE_DETAIL_CONCAT_INNER(a, b) a##b
#define PINE_DETAIL_CONCAT(a, b) PINE_DETAIL_CONCAT_INNER(a, b)
#define PINE_REGISTER_OPERATOR(SCHEMA, FACTORY)                              \
    namespace {                                                              \
        const bool PINE_DETAIL_CONCAT(_pine_reg_, __COUNTER__) = [] {        \
            ::pine::register_operator((SCHEMA), (FACTORY));                  \
            return true;                                                     \
        }();                                                                 \
    }


}  // namespace pine
