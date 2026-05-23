#include "pine/column_frame.hpp"

#include <algorithm>
#include <mutex>
#include <set>
#include <stdexcept>

namespace pine {

ColumnFrame::ColumnFrame() : items_(std::make_unique<TypedColumnStore>(0)) {}

ColumnFrame::ColumnFrame(std::map<std::string, JsonValue> common,
                         std::vector<std::map<std::string, JsonValue>> items)
    : common_(std::move(common)),
      items_(std::make_unique<TypedColumnStore>(items.size())) {
    // Collect the union of fields across items, preserving first-seen order.
    std::vector<std::string> field_order;
    std::set<std::string> seen;
    for (const auto& row : items) {
        for (const auto& [k, _] : row) {
            if (seen.insert(k).second) field_order.push_back(k);
        }
    }
    // For each field, infer column type from present non-null values and
    // then fill row by row, distinguishing ABSENT (field missing in row,
    // append_null → validity=false) from PRESENT-NULL (field present with
    // null value — requires JsonColumn so the null is kept as a value).
    for (const auto& field : field_order) {
        bool any_present_null = false;
        std::vector<JsonValue> present_values;
        for (const auto& row : items) {
            auto it = row.find(field);
            if (it == row.end()) continue;
            if (it->second.is_null()) any_present_null = true;
            else present_values.push_back(it->second);
        }

        std::unique_ptr<Column> col;
        if (any_present_null) {
            col = std::make_unique<JsonColumn>();
        } else {
            // Probe type via a sample column; we only need its type tag.
            auto sample = make_column(present_values);
            switch (sample->type()) {
                case ColumnType::Int64:  col = std::make_unique<Int64Column>();  break;
                case ColumnType::Double: col = std::make_unique<DoubleColumn>(); break;
                case ColumnType::String: col = std::make_unique<StringColumn>(); break;
                case ColumnType::Bool:   col = std::make_unique<BoolColumn>();   break;
                case ColumnType::Json:   col = std::make_unique<JsonColumn>();   break;
            }
        }

        for (const auto& row : items) {
            auto it = row.find(field);
            if (it == row.end()) {
                col->append_null();
            } else if (!col->append(it->second)) {
                col = col->to_json_column();
                col->append(it->second);
            }
        }
        items_->set_column(field, std::move(col));
    }
}

std::unique_ptr<ColumnFrame> ColumnFrame::make_window_view(
    const ColumnFrame& parent,
    std::size_t row_offset,
    std::size_t row_count) {
    // The CONTRACT in column_frame.hpp says parent is read-only during the
    // view's lifetime. Read parent.items_ unlocked: the only writer that
    // could race is apply_output, and parallel_execute (the sole caller)
    // guarantees the parent frame is not being mutated for the duration.
    const std::size_t parent_rows = parent.items_ ? parent.items_->row_count() : 0;
    if (row_offset + row_count > parent_rows) {
        throw Error("ColumnFrame::make_window_view: window ("
                    + std::to_string(row_offset) + ", "
                    + std::to_string(row_count) + ") exceeds parent row count "
                    + std::to_string(parent_rows));
    }
    auto v = std::unique_ptr<ColumnFrame>(new ColumnFrame());
    // Drop the empty owned items_ allocated by the default ctor — view
    // never reads its own items_; reads route through view_items_.
    v->items_.reset();
    v->view_common_ = &parent.common_;
    v->view_items_  = parent.items_.get();
    v->view_offset_ = row_offset;
    v->view_count_  = row_count;
    v->resources_   = parent.resources_;
    return v;
}

JsonValue ColumnFrame::common(const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    const auto& src = view_common_ ? *view_common_ : common_;
    auto it = src.find(field);
    if (it == src.end()) return JsonValue();
    return it->second;
}

bool ColumnFrame::has_common(const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    const auto& src = view_common_ ? *view_common_ : common_;
    auto it = src.find(field);
    return it != src.end() && !it->second.is_null();
}

void ColumnFrame::set_common(const std::string& field, JsonValue value) {
    if (is_window_view()) {
        throw Error("ColumnFrame::set_common called on window view "
                    "(parallel shard contract violation)");
    }
    std::unique_lock<std::shared_mutex> lk(mu_);
    common_[field] = std::move(value);
}

std::vector<std::string> ColumnFrame::common_fields() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    const auto& src = view_common_ ? *view_common_ : common_;
    std::vector<std::string> out;
    out.reserve(src.size());
    for (const auto& [k, _] : src) out.push_back(k);
    return out;
}

std::size_t ColumnFrame::item_count() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    return view_items_ ? view_count_ : items_->row_count();
}

JsonValue ColumnFrame::item(std::size_t index, const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    const ColumnStore* store = view_items_ ? view_items_ : items_.get();
    if (view_items_) {
        if (index >= view_count_) return JsonValue();
        index += view_offset_;
    } else if (index >= store->row_count()) {
        return JsonValue();
    }
    const Column* col = store->column(field);
    if (!col) return JsonValue();
    if (col->is_null(index)) return JsonValue();
    return col->get(index);
}

bool ColumnFrame::item_has(std::size_t index, const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    const ColumnStore* store = view_items_ ? view_items_ : items_.get();
    if (view_items_) {
        if (index >= view_count_) return false;
        index += view_offset_;
    } else if (index >= store->row_count()) {
        return false;
    }
    const Column* col = store->column(field);
    if (!col) return false;
    return !col->is_null(index);
}

std::vector<std::string> ColumnFrame::item_fields() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    const ColumnStore* store = view_items_ ? view_items_ : items_.get();
    return store->fields();
}

void ColumnFrame::push_warning(std::string msg) {
    if (is_window_view()) {
        throw Error("ColumnFrame::push_warning called on window view "
                    "(parallel shard contract violation)");
    }
    std::unique_lock<std::shared_mutex> lk(mu_);
    warnings_.push_back(std::move(msg));
}

std::vector<std::string> ColumnFrame::take_warnings() {
    std::unique_lock<std::shared_mutex> lk(mu_);
    return std::move(warnings_);
}

void ColumnFrame::write_item_field_locked(std::size_t idx,
                                          const std::string& field,
                                          const JsonValue& value) {
    Column* col = items_->mutate_column(field);
    if (!col) {
        // New column — start as JsonColumn so any value type (including
        // explicit nulls) can be stored without further promotion.
        auto new_col = std::make_unique<JsonColumn>(items_->row_count());
        items_->set_column(field, std::move(new_col));
        col = items_->mutate_column(field);
    }
    // A null write must mark the slot as present-null. Typed columns
    // cannot represent that, so promote to JsonColumn first.
    if (value.is_null() && col->type() != ColumnType::Json) {
        auto promoted = col->to_json_column();
        items_->set_column(field, std::move(promoted));
        col = items_->mutate_column(field);
    }
    if (!col->set(idx, value)) {
        // Type mismatch: promote to JsonColumn and retry.
        auto promoted = col->to_json_column();
        promoted->set(idx, value);
        items_->set_column(field, std::move(promoted));
    }
}

void ColumnFrame::apply_output(const OperatorOutput& out,
                               const std::string& op_name,
                               bool is_recall) {
    if (is_window_view()) {
        throw Error("ColumnFrame::apply_output called on window view "
                    "(parallel shard contract violation)");
    }
    std::unique_lock<std::shared_mutex> lk(mu_);

    // 1. common writes
    for (const auto& [field, value] : out.common_writes()) {
        common_[field] = value;
    }

    // 2. item writes
    for (const auto& [idx, field, value] : out.item_writes()) {
        if (idx < 0 || static_cast<std::size_t>(idx) >= items_->row_count()) {
            throw ExecutionError(op_name, "SetItem index " +
                                 std::to_string(idx) + " out of range [0, " +
                                 std::to_string(items_->row_count()) + ")");
        }
        write_item_field_locked(static_cast<std::size_t>(idx), field, value);
    }

    // 3. removals
    if (!out.removed_items().empty()) {
        const auto& removed = out.removed_items();
        for (int idx : removed) {
            if (idx < 0 || static_cast<std::size_t>(idx) >= items_->row_count()) {
                throw ExecutionError(op_name, "RemoveItem index " +
                                     std::to_string(idx) + " out of range [0, " +
                                     std::to_string(items_->row_count()) + ")");
            }
        }
        items_->remove_rows(removed);
    }

    // 4. reorder
    if (out.has_item_order()) {
        const auto& order = out.item_order();
        if (order.size() != items_->row_count()) {
            throw ExecutionError(op_name, "SetItemOrder length " +
                                 std::to_string(order.size()) +
                                 " does not match item count " +
                                 std::to_string(items_->row_count()));
        }
        // Validate every index is in [0, row_count) AND that the order is a
        // true permutation (each index appears exactly once). Without the
        // permutation check, `set_item_order([0,0,0])` silently makes every
        // item a copy of item 0 — a data-loss bug with no observable error.
        std::vector<bool> seen(items_->row_count(), false);
        for (int idx : order) {
            if (idx < 0 || static_cast<std::size_t>(idx) >= items_->row_count()) {
                throw ExecutionError(op_name, "SetItemOrder index " +
                                     std::to_string(idx) + " out of range [0, " +
                                     std::to_string(items_->row_count()) + ")");
            }
            if (seen[idx]) {
                throw ExecutionError(op_name, "SetItemOrder duplicate index " +
                                     std::to_string(idx) +
                                     " (order must be a permutation)");
            }
            seen[idx] = true;
        }
        items_->reorder_rows(order);
    }

    // 5. additions
    if (!out.added_items().empty()) {
        std::size_t base = items_->row_count();
        items_->extend_rows(out.added_items().size());
        for (std::size_t i = 0; i < out.added_items().size(); ++i) {
            const auto& added = out.added_items()[i];
            for (const auto& [field, value] : added) {
                write_item_field_locked(base + i, field, value);
            }
            if (is_recall) {
                write_item_field_locked(base + i, "_source", JsonValue(op_name));
            }
        }
    }

    // 6. warning (collected per-operator on the frame — every warning recorded).
    // Mirrors pine-go pine.go:246 (`fmt.Errorf("operator %q: %w", w.Operator, w.Err)`):
    // operator code emits the bare message; the engine layer prepends the
    // `operator "name": ` prefix uniformly here.
    if (out.has_warning()) {
        warnings_.push_back("operator \"" + op_name + "\": " + out.warning());
    }
}

Result ColumnFrame::to_result(const std::vector<std::string>& common_out,
                              const std::vector<std::string>& item_out) const {
    // Window views are read-only temporary projections meant for parallel
    // shard dispatch; to_result is the response-rendering surface and only
    // makes sense on the master frame after apply_output. Reject early to
    // surface programming errors before they segfault on the null items_.
    // (review #8 R8-1)
    if (is_window_view()) {
        throw Error("ColumnFrame::to_result called on window view "
                    "(window views are read-only shard projections, "
                    "not response sources)");
    }
    std::shared_lock<std::shared_mutex> lk(mu_);
    Result r;
    for (const auto& field : common_out) {
        auto it = common_.find(field);
        if (it != common_.end()) {
            r.common[field] = it->second;
        }
    }
    r.items.reserve(items_->row_count());
    for (std::size_t i = 0; i < items_->row_count(); ++i) {
        std::map<std::string, JsonValue> row;
        for (const auto& field : item_out) {
            const Column* col = items_->column(field);
            if (col && col->is_present(i)) {
                row[field] = col->get(i);
            }
        }
        r.items.push_back(std::move(row));
    }
    return r;
}

JsonValue::object_t ColumnFrame::item_object(std::size_t index) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    JsonValue::object_t out;
    const ColumnStore* store = view_items_ ? view_items_ : items_.get();
    if (view_items_) {
        if (index >= view_count_) return out;
        index += view_offset_;
    } else if (index >= store->row_count()) {
        return out;
    }
    for (const auto& field : store->fields()) {
        const Column* col = store->column(field);
        if (col && col->is_present(index)) {
            out[field] = col->get(index);
        }
    }
    return out;
}

}  // namespace pine
