#include "pine/column.hpp"

#include <algorithm>
#include <cmath>
#include <stdexcept>

namespace pine {

const char* column_type_name(ColumnType t) {
  switch (t) {
    case ColumnType::Int64:
      return "int64";
    case ColumnType::Double:
      return "double";
    case ColumnType::String:
      return "string";
    case ColumnType::Bool:
      return "bool";
    case ColumnType::Json:
      return "json";
  }
  return "unknown";
}

namespace {

bool is_integral_number(const Variant& v) {
  if (!v.is_number()) {
    return false;
  }
  double d = v.as_number();
  return std::isfinite(d) && std::trunc(d) == d && d > -9.2233720368547758e18 && d < 9.2233720368547758e18;
}

// Build a dense bitmap from a sparse set for O(1) membership test.
std::vector<bool> make_remove_bitmap(std::size_t n, const std::set<int>& indices) {
  std::vector<bool> bitmap(n, false);
  for (int idx : indices) {
    bitmap[static_cast<std::size_t>(idx)] = true;
  }
  return bitmap;
}

// Compact `data` in-place: keep slots where bitmap[i]==false, drop where
// bitmap[i]==true. `kept_count` is the resulting size (caller has it
// already so we skip recomputing). Used uniformly by all column types.
template <typename V>
void compact_with_bitmap(std::vector<V>& data, const std::vector<bool>& bitmap, std::size_t kept_count) {
  std::size_t write = 0;
  for (std::size_t i = 0; i < data.size(); ++i) {
    if (!bitmap[i]) {
      if (write != i) {
        data[write] = std::move(data[i]);
      }
      ++write;
    }
  }
  data.resize(kept_count);
}

// vector<bool> needs its own overload — packed bits don't move, and
// reading data[i] after possibly-overwriting data[write] is safe because
// vector<bool>::reference reads the bit fresh each access.
void compact_with_bitmap(std::vector<bool>& data, const std::vector<bool>& bitmap, std::size_t kept_count) {
  std::size_t write = 0;
  for (std::size_t i = 0; i < data.size(); ++i) {
    if (!bitmap[i]) {
      if (write != i) {
        data[write] = data[i];
      }
      ++write;
    }
  }
  data.resize(kept_count);
}

// Legacy std::set entry point: build the bitmap then delegate. Kept for
// callers that haven't been migrated to the bitmap path (e.g. unit
// tests). The hot store path computes the bitmap once at the store
// layer and calls remove_with_bitmap directly, sharing it across all
// columns.
template <typename T>
void remove_via_set(std::vector<T>& data, std::vector<bool>& validity, const std::set<int>& indices) {
  if (indices.empty()) {
    return;
  }
  auto bitmap = make_remove_bitmap(data.size(), indices);
  const std::size_t kept = data.size() - indices.size();
  compact_with_bitmap(data, bitmap, kept);
  compact_with_bitmap(validity, bitmap, kept);
}

// In-place permutation via cycle following. `order` MUST be a valid
// length-n permutation of [0, n) — the caller (ColumnFrame::apply_output)
// validates this before invoking reorder. Each cycle of the permutation
// is walked once, performing ≤ n moves total with zero heap allocations.
// Replaces the naive "build a fresh vector and copy from old slots" loop
// which paid N element-copies (Variant copy-ctor is expensive for the
// JsonColumn case) plus a vector realloc + old-vector teardown.
template <typename T>
void reorder_in_place(std::vector<T>& data, std::vector<bool>& validity, const std::vector<int>& order) {
  const std::size_t n = order.size();
  if (n == 0) {
    return;
  }
  std::vector<bool> visited(n, false);
  for (std::size_t i = 0; i < n; ++i) {
    if (visited[i]) {
      continue;
    }
    if (static_cast<std::size_t>(order[i]) == i) {
      visited[i] = true;
      continue;
    }
    T tmp = std::move(data[i]);
    bool tmp_valid = validity[i];
    std::size_t j = i;
    while (true) {
      const std::size_t src = static_cast<std::size_t>(order[j]);
      if (src == i) {
        data[j] = std::move(tmp);
        validity[j] = tmp_valid;
        visited[j] = true;
        break;
      }
      data[j] = std::move(data[src]);
      validity[j] = validity[src];
      visited[j] = true;
      j = src;
    }
  }
}

}  // namespace

// ---------------- TypedColumn ----------------

template <>
ColumnType Int64Column::type() const {
  return ColumnType::Int64;
}
template <>
ColumnType DoubleColumn::type() const {
  return ColumnType::Double;
}
template <>
ColumnType StringColumn::type() const {
  return ColumnType::String;
}
template <>
ColumnType BoolColumn::type() const {
  return ColumnType::Bool;
}

template <>
Variant Int64Column::get(std::size_t i) const {
  if (is_null(i)) {
    return Variant();
  }
  // Int64Column stores the original int64 precisely, but Variant only
  // carries double — for |v| > 2^53 the returned value will not round-trip.
  // pine-go / pine-java / pine-python store numeric columns as double
  // natively, so the loss is symmetric across runtimes; the precision
  // boundary is documented here and pine::int64_lossy_as_double()
  // exposes the detection seam.
  return Variant(static_cast<double>(data_[i]));
}
template <>
Variant DoubleColumn::get(std::size_t i) const {
  if (is_null(i)) {
    return Variant();
  }
  return Variant(data_[i]);
}
template <>
Variant StringColumn::get(std::size_t i) const {
  if (is_null(i)) {
    return Variant();
  }
  return Variant(data_[i]);
}
template <>
Variant BoolColumn::get(std::size_t i) const {
  if (is_null(i)) {
    return Variant();
  }
  return Variant(data_[i]);
}

template <>
bool Int64Column::set(std::size_t i, const Variant& v) {
  if (v.is_null()) {
    return false;  // typed cannot hold present-null; caller promotes.
  }
  if (!is_integral_number(v)) {
    return false;
  }
  if (i >= data_.size()) {
    return false;
  }
  data_[i] = static_cast<int64_t>(v.as_number());
  validity_[i] = true;
  return true;
}
template <>
bool DoubleColumn::set(std::size_t i, const Variant& v) {
  if (v.is_null()) {
    return false;
  }
  if (!v.is_number()) {
    return false;
  }
  if (i >= data_.size()) {
    return false;
  }
  data_[i] = v.as_number();
  validity_[i] = true;
  return true;
}
template <>
bool StringColumn::set(std::size_t i, const Variant& v) {
  if (v.is_null()) {
    return false;
  }
  if (!v.is_string()) {
    return false;
  }
  if (i >= data_.size()) {
    return false;
  }
  data_[i] = v.as_string();
  validity_[i] = true;
  return true;
}
template <>
bool BoolColumn::set(std::size_t i, const Variant& v) {
  if (v.is_null()) {
    return false;
  }
  if (!v.is_bool()) {
    return false;
  }
  if (i >= data_.size()) {
    return false;
  }
  data_[i] = v.as_bool();
  validity_[i] = true;
  return true;
}

template <>
bool Int64Column::append(const Variant& v) {
  if (v.is_null()) {
    return false;
  }
  if (!is_integral_number(v)) {
    return false;
  }
  data_.push_back(static_cast<int64_t>(v.as_number()));
  validity_.push_back(true);
  return true;
}
template <>
bool DoubleColumn::append(const Variant& v) {
  if (v.is_null()) {
    return false;
  }
  if (!v.is_number()) {
    return false;
  }
  data_.push_back(v.as_number());
  validity_.push_back(true);
  return true;
}
template <>
bool StringColumn::append(const Variant& v) {
  if (v.is_null()) {
    return false;
  }
  if (!v.is_string()) {
    return false;
  }
  data_.push_back(v.as_string());
  validity_.push_back(true);
  return true;
}
template <>
bool BoolColumn::append(const Variant& v) {
  if (v.is_null()) {
    return false;
  }
  if (!v.is_bool()) {
    return false;
  }
  data_.push_back(v.as_bool());
  validity_.push_back(true);
  return true;
}

template <typename T>
void TypedColumn<T>::append_null() {
  data_.push_back(T{});
  validity_.push_back(false);
}

template <typename T>
void TypedColumn<T>::remove(const std::set<int>& indices) {
  remove_via_set(data_, validity_, indices);
}

template <typename T>
void TypedColumn<T>::remove_with_bitmap(const std::vector<bool>& bitmap, std::size_t kept_count) {
  compact_with_bitmap(data_, bitmap, kept_count);
  compact_with_bitmap(validity_, bitmap, kept_count);
}

template <typename T>
void TypedColumn<T>::reorder(const std::vector<int>& order) {
  reorder_in_place(data_, validity_, order);
}

template <typename T>
std::unique_ptr<Column> TypedColumn<T>::clone() const {
  auto c = std::make_unique<TypedColumn<T>>();
  c->data_ = data_;
  c->validity_ = validity_;
  return c;
}

template <typename T>
std::unique_ptr<Column> TypedColumn<T>::to_json_column() const {
  auto c = std::make_unique<JsonColumn>();
  for (std::size_t i = 0; i < data_.size(); ++i) {
    if (validity_[i]) {
      c->append(this->get(i));
    } else {
      c->append_null();
    }
  }
  return c;
}

template class TypedColumn<int64_t>;
template class TypedColumn<double>;
template class TypedColumn<std::string>;
template class TypedColumn<bool>;

// ---------------- JsonColumn ----------------

Variant JsonColumn::get(std::size_t i) const {
  if (i >= validity_.size() || !validity_[i]) {
    return Variant();
  }
  return data_[i];
}

bool JsonColumn::set(std::size_t i, const Variant& v) {
  if (i >= data_.size()) {
    return false;
  }
  // Present-null is allowed: store the null value and mark present=true.
  data_[i] = v;
  validity_[i] = true;
  return true;
}

bool JsonColumn::append(const Variant& v) {
  // append always marks the new slot as present (value may itself be null).
  data_.push_back(v);
  validity_.push_back(true);
  return true;
}

void JsonColumn::append_null() {
  data_.push_back(Variant());
  validity_.push_back(false);
}

void JsonColumn::remove(const std::set<int>& indices) {
  remove_via_set(data_, validity_, indices);
}

void JsonColumn::remove_with_bitmap(const std::vector<bool>& bitmap, std::size_t kept_count) {
  compact_with_bitmap(data_, bitmap, kept_count);
  compact_with_bitmap(validity_, bitmap, kept_count);
}

void JsonColumn::reorder(const std::vector<int>& order) {
  reorder_in_place(data_, validity_, order);
}

std::unique_ptr<Column> JsonColumn::clone() const {
  auto c = std::make_unique<JsonColumn>();
  c->data_ = data_;
  c->validity_ = validity_;
  return c;
}

std::unique_ptr<Column> JsonColumn::to_json_column() const {
  return clone();
}

// ---------------- Factories ----------------

std::unique_ptr<Column> make_column(const std::vector<Variant>& values) {
  // Probe non-null values for a homogeneous type.
  enum class Cand { Unknown, Int64, Double, String, Bool, Json };
  Cand cand = Cand::Unknown;
  for (const auto& v : values) {
    if (v.is_null()) {
      continue;
    }
    Cand c;
    if (v.is_bool()) {
      c = Cand::Bool;
    } else if (v.is_number()) {
      c = is_integral_number(v) ? Cand::Int64 : Cand::Double;
    } else if (v.is_string()) {
      c = Cand::String;
    } else {
      c = Cand::Json;
    }

    if (cand == Cand::Unknown) {
      cand = c;
      continue;
    }
    if (cand == c) {
      continue;
    }
    // Allow Int64 ↔ Double widening.
    if ((cand == Cand::Int64 && c == Cand::Double) || (cand == Cand::Double && c == Cand::Int64)) {
      cand = Cand::Double;
      continue;
    }
    cand = Cand::Json;
    break;
  }

  std::unique_ptr<Column> col;
  switch (cand) {
    case Cand::Int64:
      col = std::make_unique<Int64Column>();
      break;
    case Cand::Double:
      col = std::make_unique<DoubleColumn>();
      break;
    case Cand::String:
      col = std::make_unique<StringColumn>();
      break;
    case Cand::Bool:
      col = std::make_unique<BoolColumn>();
      break;
    default:
      col = std::make_unique<JsonColumn>();
      break;
  }
  for (const auto& v : values) {
    if (!col->append(v)) {
      // Should not happen given the probe above, but guard anyway.
      col = col->to_json_column();
      col->append(v);
    }
  }
  return col;
}

std::unique_ptr<Column> make_null_column(std::size_t n) {
  auto c = std::make_unique<JsonColumn>(n);
  return c;
}

}  // namespace pine
