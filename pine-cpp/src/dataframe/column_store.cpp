#include "pine/column_store.hpp"

#include <stdexcept>

namespace pine {

std::vector<std::string> TypedColumnStore::fields() const {
  std::vector<std::string> out;
  out.reserve(cols_.size());
  for (const auto& [k, _] : cols_) {
    out.push_back(k);
  }
  return out;
}

bool TypedColumnStore::has_column(const std::string& field) const {
  return cols_.count(field) > 0;
}

const Column* TypedColumnStore::column(const std::string& field) const {
  auto it = cols_.find(field);
  return it == cols_.end() ? nullptr : it->second.get();
}

void TypedColumnStore::set_column(const std::string& field, std::unique_ptr<Column> col) {
  if (!col) {
    throw std::invalid_argument("TypedColumnStore::set_column: null column");
  }
  if (cols_.empty() && col->size() != row_count_) {
    // First column establishes row_count if store is empty.
    if (row_count_ == 0) {
      row_count_ = col->size();
    } else if (col->size() != row_count_) {
      throw std::invalid_argument("TypedColumnStore::set_column: column size mismatch");
    }
  } else if (col->size() != row_count_) {
    throw std::invalid_argument("TypedColumnStore::set_column: column size mismatch");
  }
  cols_[field] = std::move(col);
}

Column* TypedColumnStore::mutate_column(const std::string& field) {
  auto it = cols_.find(field);
  return it == cols_.end() ? nullptr : it->second.get();
}

void TypedColumnStore::remove_rows(const std::set<int>& indices) {
  if (indices.empty()) {
    return;
  }
  // Validate OOB at the public-API boundary so any caller (apply_output
  // path or future direct consumers) gets a consistent error instead of
  // silently corrupting `row_count_` vs column-internal sizes. The
  // ColumnFrame::apply_output path pre-validates, but ColumnStore is a
  // public surface and other callers (Arrow store, COW snapshot, ...)
  // would otherwise step into the same trap.
  for (int i : indices) {
    if (i < 0 || static_cast<std::size_t>(i) >= row_count_) {
      throw std::invalid_argument("TypedColumnStore::remove_rows: index " + std::to_string(i) +
                                  " out of range [0, " + std::to_string(row_count_) + ")");
    }
  }
  for (auto& [_, col] : cols_) {
    col->remove(indices);
  }
  row_count_ -= indices.size();
}

void TypedColumnStore::reorder_rows(const std::vector<int>& order) {
  if (order.size() != row_count_) {
    throw std::invalid_argument("TypedColumnStore::reorder_rows: order length mismatch");
  }
  for (auto& [_, col] : cols_) {
    col->reorder(order);
  }
}

void TypedColumnStore::extend_rows(std::size_t n) {
  if (n == 0) {
    return;
  }
  for (auto& [_, col] : cols_) {
    for (std::size_t i = 0; i < n; ++i) {
      col->append_null();
    }
  }
  row_count_ += n;
}

std::unique_ptr<ColumnStore> TypedColumnStore::clone() const {
  auto out = std::make_unique<TypedColumnStore>(row_count_);
  for (const auto& [k, v] : cols_) {
    out->cols_[k] = v->clone();
  }
  return out;
}

}  // namespace pine
