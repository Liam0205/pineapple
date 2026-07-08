#pragma once

#include "pine/pine.hpp"

#include <cstdint>
#include <memory>
#include <set>
#include <string>
#include <vector>

namespace pine {

// ColumnType identifies the underlying storage type of a typed column.
// Json is the fallback for mixed/object/array values where typed storage
// is not possible.
enum class ColumnType { Int64, Double, String, Bool, Json };

const char* column_type_name(ColumnType t);

// Column is the abstract type-erased base for a single field's data.
// All columns carry a presence bitmap so positions can be ABSENT (the row
// never wrote this field) even when the underlying typed storage is
// fixed-width. JsonColumn additionally allows PRESENT-NULL (the row
// explicitly wrote a null value) — typed columns cannot represent that
// state and must be promoted to JsonColumn first.
//
// Mutating operations (set / append / remove / reorder) operate in-place
// and require unique ownership. Sharing immutable snapshots is done via
// std::shared_ptr<const Column> at the ColumnStore layer — see decision 7.
//
// Type-mismatched writes (e.g., string into an Int64Column, or any null
// write into a typed column) return false; the caller is expected to
// promote the column to a JsonColumn first via to_json_column().
class Column {
 public:
  virtual ~Column() = default;

  virtual ColumnType type() const = 0;
  virtual std::size_t size() const = 0;
  // is_null is the user-facing "value at i is nil" predicate. Returns
  // true when the slot is ABSENT or when the stored value is Variant
  // null (only possible on JsonColumn).
  virtual bool is_null(std::size_t i) const = 0;
  // is_present reflects the raw presence bit: true iff the row
  // explicitly wrote this field (the written value may itself be null
  // for JsonColumn).
  virtual bool is_present(std::size_t i) const = 0;
  virtual Variant get(std::size_t i) const = 0;

  virtual bool set(std::size_t i, const Variant& v) = 0;
  virtual bool append(const Variant& v) = 0;
  virtual void append_null() = 0;
  virtual void remove(const std::set<int>& indices) = 0;
  // Bitmap-driven remove. `bitmap[i] == true` means row i is dropped;
  // `kept_count` is the resulting size (caller-computed once per store-
  // wide remove so K columns share the same bitmap, avoiding K set-to-
  // bitmap conversions). See TypedColumnStore::remove_rows.
  virtual void remove_with_bitmap(const std::vector<bool>& bitmap, std::size_t kept_count) = 0;
  // Cycle-following in-place permutation. `visited_scratch` is workspace
  // borrowed from the caller: ColumnStore::reorder_rows allocates it
  // once and shares the buffer across all K columns, replacing K
  // independent vector<bool>(n) allocations per reorder call (mirrors
  // remove_with_bitmap's K-column sharing). The column resets the
  // scratch before use, so the caller is free to reuse without clearing.
  virtual void reorder(const std::vector<int>& order, std::vector<bool>& visited_scratch) = 0;

  // Convenience overload for standalone single-column callers (tests,
  // direct API users) where the K-column amortization does not apply.
  // Allocates a one-shot scratch and forwards to the workspace-aware
  // virtual. Non-virtual on purpose — implementations override the
  // 2-arg form only.
  void reorder(const std::vector<int>& order) {
    std::vector<bool> scratch;
    reorder(order, scratch);
  }

  virtual std::unique_ptr<Column> clone() const = 0;
  // Materialize this column as an equivalent JsonColumn (used when a
  // type-incompatible value or a present-null forces promotion).
  virtual std::unique_ptr<Column> to_json_column() const = 0;
};

// TypedColumn<T> stores fixed-width scalar data with a parallel validity
// bitmap. Specialized for int64_t, double, std::string, bool.
template <typename T>
class TypedColumn final : public Column {
 public:
  TypedColumn() = default;
  explicit TypedColumn(std::size_t n) : data_(n), validity_(n, false) {
  }

  ColumnType type() const override;
  std::size_t size() const override {
    return data_.size();
  }
  // Re-import the base-class 1-arg reorder so direct calls on a TypedColumn
  // static type don't get hidden by the 2-arg override below (C++ name
  // hiding). All current callers go through a Column* / unique_ptr<Column>
  // so this is defense in depth, but it removes the footgun for future
  // typed-path consumers.
  using Column::reorder;
  bool is_null(std::size_t i) const override {
    return i >= validity_.size() || !validity_[i];
  }
  bool is_present(std::size_t i) const override {
    return i < validity_.size() && validity_[i];
  }
  Variant get(std::size_t i) const override;

  bool set(std::size_t i, const Variant& v) override;
  bool append(const Variant& v) override;
  void append_null() override;
  void remove(const std::set<int>& indices) override;
  void remove_with_bitmap(const std::vector<bool>& bitmap, std::size_t kept_count) override;
  void reorder(const std::vector<int>& order, std::vector<bool>& visited_scratch) override;

  std::unique_ptr<Column> clone() const override;
  std::unique_ptr<Column> to_json_column() const override;

  // Direct access for testing / typed paths.
  const std::vector<T>& data() const {
    return data_;
  }
  const std::vector<bool>& validity() const {
    return validity_;
  }

  // Takes ownership of vals as the full column, all slots present
  // (write-side zero-copy counterpart of data()). Used by
  // ColumnFrame::apply_output to adopt whole-column typed writes.
  void adopt(std::vector<T> vals) {
    validity_.assign(vals.size(), true);
    data_ = std::move(vals);
  }

 private:
  std::vector<T> data_;
  std::vector<bool> validity_;
};

using Int64Column = TypedColumn<int64_t>;
using DoubleColumn = TypedColumn<double>;
using StringColumn = TypedColumn<std::string>;
using BoolColumn = TypedColumn<bool>;

// int64_lossy_as_double returns true if the given int64 cannot be losslessly
// represented as IEEE 754 binary64 (i.e. |v| > 2^53). Int64Column stores
// values precisely, but get() returns Variant which only carries double —
// callers that care about user-supplied identifiers (user_id, order_id) at
// magnitudes above 9.0e15 should detect this case and either route through
// a typed path or surface a debug warning.
//
// Note: pine-go / pine-java / pine-python store numeric columns as double
// natively, so the precision loss is symmetric across runtimes; this helper
// is the pine-cpp-only seam for diagnosing the boundary.
constexpr bool int64_lossy_as_double(int64_t v) {
  constexpr int64_t k = static_cast<int64_t>(1) << 53;
  return v > k || v < -k;
}

// JsonColumn is the heterogeneous fallback: data stored as Variant.
// All set/append operations succeed regardless of value type.
class JsonColumn final : public Column {
 public:
  JsonColumn() = default;
  explicit JsonColumn(std::size_t n) : data_(n), validity_(n, false) {
  }

  ColumnType type() const override {
    return ColumnType::Json;
  }
  std::size_t size() const override {
    return data_.size();
  }
  // See TypedColumn for the rationale on re-importing the base 1-arg overload.
  using Column::reorder;
  bool is_null(std::size_t i) const override {
    return i >= validity_.size() || !validity_[i] || data_[i].is_null();
  }
  bool is_present(std::size_t i) const override {
    return i < validity_.size() && validity_[i];
  }
  Variant get(std::size_t i) const override;

  bool set(std::size_t i, const Variant& v) override;
  bool append(const Variant& v) override;
  void append_null() override;
  void remove(const std::set<int>& indices) override;
  void remove_with_bitmap(const std::vector<bool>& bitmap, std::size_t kept_count) override;
  void reorder(const std::vector<int>& order, std::vector<bool>& visited_scratch) override;

  std::unique_ptr<Column> clone() const override;
  std::unique_ptr<Column> to_json_column() const override;

 private:
  std::vector<Variant> data_;
  std::vector<bool> validity_;
};

// make_column scans `values` and returns the best-fitting Column:
//   - all ints → Int64Column
//   - all numeric (mixed int/double) → DoubleColumn
//   - all strings → StringColumn
//   - all bools → BoolColumn
//   - anything else / mixed → JsonColumn
// Nulls in the source are preserved via the validity bitmap.
// Empty inputs return a JsonColumn of size 0 (type cannot be inferred).
std::unique_ptr<Column> make_column(const std::vector<Variant>& values);

// make_null_column returns a JsonColumn of size `n`, all entries NULL.
std::unique_ptr<Column> make_null_column(std::size_t n);

}  // namespace pine
