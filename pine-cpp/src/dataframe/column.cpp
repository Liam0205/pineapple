#include "dataframe/column.hpp"

#include <algorithm>
#include <cmath>
#include <stdexcept>

namespace pine {

const char* column_type_name(ColumnType t) {
    switch (t) {
        case ColumnType::Int64:  return "int64";
        case ColumnType::Double: return "double";
        case ColumnType::String: return "string";
        case ColumnType::Bool:   return "bool";
        case ColumnType::Json:   return "json";
    }
    return "unknown";
}

namespace {

bool is_integral_number(const JsonValue& v) {
    if (!v.is_number()) return false;
    double d = v.as_number();
    return std::isfinite(d) && std::trunc(d) == d &&
           d >= -9.2233720368547758e18 && d <= 9.2233720368547758e18;
}

void apply_remove(std::vector<bool>& validity, const std::set<int>& indices) {
    std::vector<bool> out;
    out.reserve(validity.size() - indices.size());
    for (std::size_t i = 0; i < validity.size(); ++i) {
        if (!indices.count(static_cast<int>(i))) out.push_back(validity[i]);
    }
    validity = std::move(out);
}

template <typename V>
void apply_remove_data(std::vector<V>& data, const std::set<int>& indices) {
    std::vector<V> out;
    out.reserve(data.size() - indices.size());
    for (std::size_t i = 0; i < data.size(); ++i) {
        if (!indices.count(static_cast<int>(i))) out.push_back(std::move(data[i]));
    }
    data = std::move(out);
}

}  // namespace

// ---------------- TypedColumn ----------------

template <> ColumnType Int64Column::type() const  { return ColumnType::Int64; }
template <> ColumnType DoubleColumn::type() const { return ColumnType::Double; }
template <> ColumnType StringColumn::type() const { return ColumnType::String; }
template <> ColumnType BoolColumn::type() const   { return ColumnType::Bool; }

template <> JsonValue Int64Column::get(std::size_t i) const {
    if (is_null(i)) return JsonValue();
    return JsonValue(static_cast<double>(data_[i]));
}
template <> JsonValue DoubleColumn::get(std::size_t i) const {
    if (is_null(i)) return JsonValue();
    return JsonValue(data_[i]);
}
template <> JsonValue StringColumn::get(std::size_t i) const {
    if (is_null(i)) return JsonValue();
    return JsonValue(data_[i]);
}
template <> JsonValue BoolColumn::get(std::size_t i) const {
    if (is_null(i)) return JsonValue();
    return JsonValue(data_[i]);
}

template <> bool Int64Column::set(std::size_t i, const JsonValue& v) {
    if (v.is_null()) return false;  // typed cannot hold present-null; caller promotes.
    if (!is_integral_number(v)) return false;
    if (i >= data_.size()) return false;
    data_[i] = static_cast<int64_t>(v.as_number());
    validity_[i] = true;
    return true;
}
template <> bool DoubleColumn::set(std::size_t i, const JsonValue& v) {
    if (v.is_null()) return false;
    if (!v.is_number()) return false;
    if (i >= data_.size()) return false;
    data_[i] = v.as_number();
    validity_[i] = true;
    return true;
}
template <> bool StringColumn::set(std::size_t i, const JsonValue& v) {
    if (v.is_null()) return false;
    if (!v.is_string()) return false;
    if (i >= data_.size()) return false;
    data_[i] = v.as_string();
    validity_[i] = true;
    return true;
}
template <> bool BoolColumn::set(std::size_t i, const JsonValue& v) {
    if (v.is_null()) return false;
    if (!v.is_bool()) return false;
    if (i >= data_.size()) return false;
    data_[i] = v.as_bool();
    validity_[i] = true;
    return true;
}

template <> bool Int64Column::append(const JsonValue& v) {
    if (v.is_null()) return false;
    if (!is_integral_number(v)) return false;
    data_.push_back(static_cast<int64_t>(v.as_number()));
    validity_.push_back(true);
    return true;
}
template <> bool DoubleColumn::append(const JsonValue& v) {
    if (v.is_null()) return false;
    if (!v.is_number()) return false;
    data_.push_back(v.as_number());
    validity_.push_back(true);
    return true;
}
template <> bool StringColumn::append(const JsonValue& v) {
    if (v.is_null()) return false;
    if (!v.is_string()) return false;
    data_.push_back(v.as_string());
    validity_.push_back(true);
    return true;
}
template <> bool BoolColumn::append(const JsonValue& v) {
    if (v.is_null()) return false;
    if (!v.is_bool()) return false;
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
    apply_remove_data(data_, indices);
    apply_remove(validity_, indices);
}

template <typename T>
void TypedColumn<T>::reorder(const std::vector<int>& order) {
    std::vector<T> new_data;
    std::vector<bool> new_valid;
    new_data.reserve(order.size());
    new_valid.reserve(order.size());
    for (int idx : order) {
        new_data.push_back(data_[static_cast<std::size_t>(idx)]);
        new_valid.push_back(validity_[static_cast<std::size_t>(idx)]);
    }
    data_ = std::move(new_data);
    validity_ = std::move(new_valid);
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
        if (validity_[i]) c->append(this->get(i));
        else c->append_null();
    }
    return c;
}

template class TypedColumn<int64_t>;
template class TypedColumn<double>;
template class TypedColumn<std::string>;
template class TypedColumn<bool>;

// ---------------- JsonColumn ----------------

JsonValue JsonColumn::get(std::size_t i) const {
    if (i >= validity_.size() || !validity_[i]) return JsonValue();
    return data_[i];
}

bool JsonColumn::set(std::size_t i, const JsonValue& v) {
    if (i >= data_.size()) return false;
    // Present-null is allowed: store the null value and mark present=true.
    data_[i] = v;
    validity_[i] = true;
    return true;
}

bool JsonColumn::append(const JsonValue& v) {
    // append always marks the new slot as present (value may itself be null).
    data_.push_back(v);
    validity_.push_back(true);
    return true;
}

void JsonColumn::append_null() {
    data_.push_back(JsonValue());
    validity_.push_back(false);
}

void JsonColumn::remove(const std::set<int>& indices) {
    apply_remove_data(data_, indices);
    apply_remove(validity_, indices);
}

void JsonColumn::reorder(const std::vector<int>& order) {
    std::vector<JsonValue> new_data;
    std::vector<bool> new_valid;
    new_data.reserve(order.size());
    new_valid.reserve(order.size());
    for (int idx : order) {
        new_data.push_back(data_[static_cast<std::size_t>(idx)]);
        new_valid.push_back(validity_[static_cast<std::size_t>(idx)]);
    }
    data_ = std::move(new_data);
    validity_ = std::move(new_valid);
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

std::unique_ptr<Column> make_column(const std::vector<JsonValue>& values) {
    // Probe non-null values for a homogeneous type.
    enum class Cand { Unknown, Int64, Double, String, Bool, Json };
    Cand cand = Cand::Unknown;
    for (const auto& v : values) {
        if (v.is_null()) continue;
        Cand c;
        if (v.is_bool()) c = Cand::Bool;
        else if (v.is_number()) c = is_integral_number(v) ? Cand::Int64 : Cand::Double;
        else if (v.is_string()) c = Cand::String;
        else c = Cand::Json;

        if (cand == Cand::Unknown) { cand = c; continue; }
        if (cand == c) continue;
        // Allow Int64 ↔ Double widening.
        if ((cand == Cand::Int64 && c == Cand::Double) ||
            (cand == Cand::Double && c == Cand::Int64)) {
            cand = Cand::Double;
            continue;
        }
        cand = Cand::Json;
        break;
    }

    std::unique_ptr<Column> col;
    switch (cand) {
        case Cand::Int64:   col = std::make_unique<Int64Column>(); break;
        case Cand::Double:  col = std::make_unique<DoubleColumn>(); break;
        case Cand::String:  col = std::make_unique<StringColumn>(); break;
        case Cand::Bool:    col = std::make_unique<BoolColumn>(); break;
        default:            col = std::make_unique<JsonColumn>(); break;
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
