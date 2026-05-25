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

JsonValue ColumnFrame::common(const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    auto it = common_.find(field);
    if (it == common_.end()) return JsonValue();
    return it->second;
}

bool ColumnFrame::has_common(const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    auto it = common_.find(field);
    return it != common_.end() && !it->second.is_null();
}

void ColumnFrame::set_common(const std::string& field, JsonValue value) {
    std::unique_lock<std::shared_mutex> lk(mu_);
    common_[field] = std::move(value);
}

std::vector<std::string> ColumnFrame::common_fields() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    std::vector<std::string> out;
    out.reserve(common_.size());
    for (const auto& [k, _] : common_) out.push_back(k);
    return out;
}

std::size_t ColumnFrame::item_count() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    return items_->row_count();
}

JsonValue ColumnFrame::item(std::size_t index, const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    if (index >= items_->row_count()) return JsonValue();
    const Column* col = items_->column(field);
    if (!col) return JsonValue();
    if (col->is_null(index)) return JsonValue();
    return col->get(index);
}

bool ColumnFrame::item_has(std::size_t index, const std::string& field) const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    if (index >= items_->row_count()) return false;
    const Column* col = items_->column(field);
    if (!col) return false;
    return !col->is_null(index);
}

std::vector<std::string> ColumnFrame::item_fields() const {
    std::shared_lock<std::shared_mutex> lk(mu_);
    return items_->fields();
}

void ColumnFrame::push_warning(std::string msg) {
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
    std::unique_lock<std::shared_mutex> lk(mu_);

    // 1. common writes
    for (const auto& [field, value] : out.common_writes()) {
        common_[field] = value;
    }

    // 2. item writes
    for (const auto& [idx, fields] : out.item_writes()) {
        if (idx < 0 || static_cast<std::size_t>(idx) >= items_->row_count()) {
            throw ExecutionError(op_name, "SetItem index " +
                                 std::to_string(idx) + " out of range [0, " +
                                 std::to_string(items_->row_count()) + ")");
        }
        for (const auto& [field, value] : fields) {
            write_item_field_locked(static_cast<std::size_t>(idx), field, value);
        }
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
    if (index >= items_->row_count()) return out;
    for (const auto& field : items_->fields()) {
        const Column* col = items_->column(field);
        if (col && col->is_present(index)) {
            out[field] = col->get(index);
        }
    }
    return out;
}

}  // namespace pine
