#include "pine/row_frame.hpp"

#include "pine/column_frame.hpp"

#include <algorithm>
#include <cmath>
#include <mutex>
#include <set>

namespace pine {

namespace {
std::string validate_value_row(const std::string& field, const Variant& value) {
  if (value.is_null()) {
    return "";
  }
  if (value.is_number()) {
    double d = value.as_number();
    if (std::isnan(d) || std::isinf(d)) {
      return "field \"" + field + "\": NaN/Inf is not a valid JSON value";
    }
  }
  return "";
}
}  // namespace

RowFrame::RowFrame() = default;

RowFrame::RowFrame(Variant::object_t common, std::vector<Variant::object_t> items)
    : common_(std::move(common)) {
  items_.reserve(items.size());
  for (auto& row : items) {
    items_.emplace_back(std::move(row));
  }
}

Variant RowFrame::common(const std::string& field) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  const auto& src = view_common_ ? *view_common_ : common_;
  auto it = src.find(field);
  if (it == src.end()) {
    return Variant();
  }
  return it->second;
}

bool RowFrame::has_common(const std::string& field) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  const auto& src = view_common_ ? *view_common_ : common_;
  auto it = src.find(field);
  return it != src.end();
}

void RowFrame::set_common(const std::string& field, Variant value) {
  if (is_window_view()) {
    throw Error(
        "RowFrame::set_common called on window view "
        "(parallel shard contract violation)");
  }
  std::unique_lock<std::shared_mutex> lk(mu_);
  common_[field] = std::move(value);
}

std::vector<std::string> RowFrame::common_fields() const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  const auto& src = view_common_ ? *view_common_ : common_;
  std::vector<std::string> out;
  out.reserve(src.size());
  for (const auto& [k, _] : src) {
    out.push_back(k);
  }
  std::sort(out.begin(), out.end());
  return out;
}

std::size_t RowFrame::item_count() const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  if (view_items_) {
    return view_count_;
  }
  return items_.size();
}

Variant RowFrame::item(std::size_t index, const std::string& field) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  const auto& src = view_items_ ? *view_items_ : items_;
  if (view_items_) {
    if (index >= view_count_) {
      return Variant();
    }
    index += view_offset_;
  } else if (index >= src.size()) {
    return Variant();
  }
  auto it = src[index].find(field);
  if (it == src[index].end()) {
    return Variant();
  }
  return it->second;
}

bool RowFrame::item_has(std::size_t index, const std::string& field) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  const auto& src = view_items_ ? *view_items_ : items_;
  if (view_items_) {
    if (index >= view_count_) {
      return false;
    }
    index += view_offset_;
  } else if (index >= src.size()) {
    return false;
  }
  auto it = src[index].find(field);
  return it != src[index].end();
}

std::vector<std::string> RowFrame::item_fields() const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  const auto& src = view_items_ ? *view_items_ : items_;
  std::set<std::string> seen;
  std::vector<std::string> out;
  std::size_t begin = view_items_ ? view_offset_ : 0;
  std::size_t end = view_items_ ? (view_offset_ + view_count_) : src.size();
  for (std::size_t i = begin; i < end; ++i) {
    for (const auto& [k, _] : src[i]) {
      if (seen.insert(k).second) {
        out.push_back(k);
      }
    }
  }
  std::sort(out.begin(), out.end());
  return out;
}

void RowFrame::push_warning(std::string msg) {
  if (is_window_view()) {
    throw Error(
        "RowFrame::push_warning called on window view "
        "(parallel shard contract violation)");
  }
  std::unique_lock<std::shared_mutex> lk(mu_);
  warnings_.push_back(std::move(msg));
}

std::vector<std::string> RowFrame::take_warnings() {
  std::unique_lock<std::shared_mutex> lk(mu_);
  return std::move(warnings_);
}

void RowFrame::apply_output(const OperatorOutput& out, const std::string& op_name, bool is_recall) {
  if (is_window_view()) {
    throw Error(
        "RowFrame::apply_output called on window view "
        "(parallel shard contract violation)");
  }
  std::unique_lock<std::shared_mutex> lk(mu_);

  // 1. common writes
  for (const auto& [field, value] : out.common_writes()) {
    if (auto v = validate_value_row(field, value); !v.empty()) {
      throw ExecutionError(op_name, "common write: " + v);
    }
    common_[field] = value;
  }

  // 2. item writes
  for (const auto& [idx, field, value] : out.item_writes()) {
    if (idx < 0 || static_cast<std::size_t>(idx) >= items_.size()) {
      throw ExecutionError(op_name, "SetItem index " + std::to_string(idx) + " out of range [0, " +
                                        std::to_string(items_.size()) + ")");
    }
    if (auto v = validate_value_row(field, value); !v.empty()) {
      throw ExecutionError(op_name, "item[" + std::to_string(idx) + "] write: " + v);
    }
    items_[static_cast<std::size_t>(idx)][field] = value;
  }

  // 3. removals
  if (!out.removed_items().empty()) {
    const auto& removed = out.removed_items();
    for (int idx : removed) {
      if (idx < 0 || static_cast<std::size_t>(idx) >= items_.size()) {
        throw ExecutionError(op_name, "RemoveItem index " + std::to_string(idx) + " out of range [0, " +
                                          std::to_string(items_.size()) + ")");
      }
    }
    std::vector<Variant::object_t> kept;
    kept.reserve(items_.size() - removed.size());
    for (std::size_t i = 0; i < items_.size(); ++i) {
      if (removed.count(static_cast<int>(i)) == 0) {
        kept.push_back(std::move(items_[i]));
      }
    }
    items_ = std::move(kept);
  }

  // 4. reorder
  if (out.has_item_order()) {
    const auto& order = out.item_order();
    if (order.size() != items_.size()) {
      throw ExecutionError(op_name, "SetItemOrder length " + std::to_string(order.size()) +
                                        " does not match item count " + std::to_string(items_.size()));
    }
    std::vector<bool> seen(items_.size(), false);
    for (int idx : order) {
      if (idx < 0 || static_cast<std::size_t>(idx) >= items_.size()) {
        throw ExecutionError(op_name, "SetItemOrder index " + std::to_string(idx) + " out of range [0, " +
                                          std::to_string(items_.size()) + ")");
      }
      if (seen[idx]) {
        throw ExecutionError(op_name, "SetItemOrder duplicate index " + std::to_string(idx) +
                                          " (order must be a permutation)");
      }
      seen[idx] = true;
    }
    std::vector<Variant::object_t> reordered;
    reordered.reserve(order.size());
    for (int idx : order) {
      reordered.push_back(std::move(items_[static_cast<std::size_t>(idx)]));
    }
    items_ = std::move(reordered);
  }

  // 5. additions
  if (!out.added_items().empty()) {
    for (const auto& added : out.added_items()) {
      for (const auto& [field, value] : added) {
        if (auto v = validate_value_row(field, value); !v.empty()) {
          throw ExecutionError(op_name, "added item write: " + v);
        }
      }
      auto row = added;
      if (is_recall) {
        row["_source"] = Variant(op_name);
      }
      items_.emplace_back(std::make_move_iterator(row.begin()), std::make_move_iterator(row.end()));
    }
  }

  // 6. warning
  if (out.has_warning()) {
    warnings_.push_back("operator \"" + op_name + "\": " + out.warning());
  }
}

Result RowFrame::to_result(const std::vector<std::string>& common_out,
                           const std::vector<std::string>& item_out) const {
  if (is_window_view()) {
    throw Error(
        "RowFrame::to_result called on window view "
        "(window views are read-only shard projections, "
        "not response sources)");
  }
  std::shared_lock<std::shared_mutex> lk(mu_);
  Result r;
  r.common.reserve(common_out.size());
  for (const auto& field : common_out) {
    auto it = common_.find(field);
    if (it != common_.end()) {
      r.common[field] = it->second;
    }
  }
  r.items.reserve(items_.size());
  for (const auto& row : items_) {
    Variant::object_t out_row;
    out_row.reserve(item_out.size());
    for (const auto& field : item_out) {
      auto it = row.find(field);
      // Keep explicit nulls — pine-go RowFrame.ToResult / projectMap
      // and pine-cpp ColumnFrame.to_result (via Column::is_present)
      // both preserve PRESENT-NULL. Only ABSENT keys are stripped.
      // (Dual-impl equivalence demands the same rule.)
      if (it != row.end()) {
        out_row[field] = it->second;
      }
    }
    r.items.push_back(std::move(out_row));
  }
  return r;
}

Variant::object_t RowFrame::item_object(std::size_t index) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  Variant::object_t out;
  const auto& src = view_items_ ? *view_items_ : items_;
  if (view_items_) {
    if (index >= view_count_) {
      return out;
    }
    index += view_offset_;
  } else if (index >= src.size()) {
    return out;
  }
  for (const auto& [k, v] : src[index]) {
    if (!v.is_null()) {
      out[k] = v;
    }
  }
  return out;
}

std::unique_ptr<Frame> RowFrame::make_window_view(std::size_t row_offset, std::size_t row_count) const {
  const std::size_t parent_rows = items_.size();
  if (row_offset + row_count > parent_rows) {
    throw Error("RowFrame::make_window_view: window (" + std::to_string(row_offset) + ", " +
                std::to_string(row_count) + ") exceeds parent row count " + std::to_string(parent_rows));
  }
  auto v = std::unique_ptr<RowFrame>(new RowFrame());
  v->view_common_ = &common_;
  v->view_items_ = &items_;
  v->view_offset_ = row_offset;
  v->view_count_ = row_count;
  v->resources_ = resources_;
  return std::unique_ptr<Frame>(std::move(v));
}

std::pair<std::string, int> RowFrame::validate_strict_items(const std::vector<std::string>& fields) const {
  std::shared_lock<std::shared_mutex> lk(mu_);
  const auto& rows = view_items_ ? *view_items_ : items_;
  std::size_t offset = view_items_ ? view_offset_ : 0;
  std::size_t count = view_items_ ? view_count_ : rows.size();

  for (const auto& field : fields) {
    for (std::size_t i = 0; i < count; ++i) {
      const auto& row = rows[offset + i];
      auto it = row.find(field);
      if (it == row.end() || it->second.is_null()) {
        return {field, static_cast<int>(i)};
      }
    }
  }
  return {"", -1};
}

// Factory selecting Frame implementation by storage_mode. Unknown
// values fall back to "column" — mirrors pine-go NewFrame behavior.
std::unique_ptr<Frame> make_frame(const std::string& storage_mode, Variant::object_t common,
                                  std::vector<Variant::object_t> items) {
  if (storage_mode == "row") {
    return std::make_unique<RowFrame>(std::move(common), std::move(items));
  }
  return std::make_unique<ColumnFrame>(std::move(common), std::move(items));
}

}  // namespace pine
