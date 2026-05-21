#include "pine/pine.hpp"
#include "dataframe/column_frame.hpp"
#include "lua/lua_bridge.hpp"
#include "redis/redis_client.hpp"

#include <algorithm>
#include <atomic>
#include <charconv>
#include <chrono>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <exception>
#include <future>
#include <map>
#include <memory>
#include <mutex>
#include <set>
#include <shared_mutex>
#include <sstream>
#include <thread>

namespace pine {

void OperatorOutput::set_common(const std::string& field, JsonValue value) {
    common_writes_[field] = std::move(value);
}

void OperatorOutput::set_item(int index, const std::string& field, JsonValue value) {
    item_writes_[index][field] = std::move(value);
}

void OperatorOutput::add_item(std::map<std::string, JsonValue> fields) {
    added_items_.push_back(std::move(fields));
}

void OperatorOutput::remove_item(int index) {
    removed_items_.insert(index);
}

void OperatorOutput::set_item_order(std::vector<int> order) {
    item_order_ = std::move(order);
    has_item_order_ = true;
}

void OperatorOutput::set_warning(std::string msg) {
    if (!has_warning_) {
        warning_ = std::move(msg);
        has_warning_ = true;
    }
}

namespace {

using Row = std::map<std::string, JsonValue>;

// Frame is the runtime DataFrame, backed by ColumnFrame for typed
// column storage + validity bitmap per decisions 04/13/14.
using Frame = ColumnFrame;

JsonValue require_common(const Frame& frame, const OperatorConfig& op, const std::string& field) {
    JsonValue v = frame.common(field);
    if (!v.is_null()) return v;
    if (auto def = op.common_defaults.find(field); def != op.common_defaults.end()) return def->second;
    throw ExecutionError(op.name, "required field \"" + field + "\" is nil in common");
}

JsonValue require_item(const Frame& frame, const OperatorConfig& op, std::size_t index, const std::string& field) {
    JsonValue v = frame.item(index, field);
    if (!v.is_null()) return v;
    if (auto def = op.item_defaults.find(field); def != op.item_defaults.end()) return def->second;
    throw ExecutionError(op.name, "required field \"" + field + "\" is nil on item[" + std::to_string(index) + "]");
}

bool should_skip(const Frame& frame, const OperatorConfig& op) {
    for (const auto& field : op.skip) {
        JsonValue v = frame.common(field);
        if (!v.is_null() && v.truthy()) return true;
    }
    return false;
}

class OperatorError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

double to_double(const JsonValue& value) {
    if (value.is_bool()) throw OperatorError("cannot convert bool to float64");
    if (value.is_number()) return value.as_number();
    if (value.is_null()) throw OperatorError("cannot convert null to double");
    if (value.is_string()) throw OperatorError("cannot convert string to double");
    if (value.is_array()) throw OperatorError("cannot convert array to double");
    throw OperatorError("cannot convert object to double");
}

void run_transform_copy(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    const auto direction = op.params.as_object().at("direction").as_string();
    if (direction == "common_to_item") {
        std::set<std::string> skip_set(op.skip.begin(), op.skip.end());
        std::vector<std::string> data_inputs;
        for (const auto& field : op.metadata.common_input) {
            if (!skip_set.count(field)) data_inputs.push_back(field);
        }
        for (std::size_t i = 0; i < data_inputs.size(); ++i) {
            JsonValue value = require_common(frame, op, data_inputs[i]);
            const auto& dst = op.metadata.item_output.at(i);
            for (std::size_t j = 0; j < frame.item_count(); ++j) {
                out.set_item(static_cast<int>(j), dst, value);
            }
        }
    } else if (direction == "common_to_common") {
        for (std::size_t i = 0; i < op.metadata.common_input.size(); ++i) {
            JsonValue value = require_common(frame, op, op.metadata.common_input[i]);
            out.set_common(op.metadata.common_output.at(i), value);
        }
    } else if (direction == "item_to_item") {
        for (std::size_t i = 0; i < op.metadata.item_input.size(); ++i) {
            const auto& src = op.metadata.item_input[i];
            const auto& dst = op.metadata.item_output.at(i);
            for (std::size_t j = 0; j < frame.item_count(); ++j) {
                out.set_item(static_cast<int>(j), dst, require_item(frame, op, j, src));
            }
        }
    } else if (direction == "item_to_common") {
        for (std::size_t i = 0; i < op.metadata.item_input.size(); ++i) {
            const auto& src = op.metadata.item_input[i];
            JsonValue::array_t vals;
            for (std::size_t j = 0; j < frame.item_count(); ++j) {
                vals.push_back(frame.item(j, src));
            }
            out.set_common(op.metadata.common_output.at(i), JsonValue(vals));
        }
    } else {
        throw ExecutionError(op.name, "transform_copy: unsupported direction \"" + direction + "\"");
    }
}

void run_transform_dispatch(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    const auto& src = op.metadata.common_input.at(0);
    const auto& dst = op.metadata.item_output.at(0);
    JsonValue val = require_common(frame, op, src);
    for (std::size_t j = 0; j < frame.item_count(); ++j) {
        out.set_item(static_cast<int>(j), dst, val);
    }
}

void run_transform_size(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    out.set_common(op.metadata.common_output.at(0), JsonValue(static_cast<double>(frame.item_count())));
}

// go_format_g formats a double matching Go's fmt.Sprintf("%g", x) byte-for-byte.
// Go rule (strconv.FormatFloat, 'g', prec=-1): take the shortest decimal digits,
// let exp = (decimal-point position) - 1. If exp < -4 OR exp >= 6, use scientific
// (d.ddde±NN with min two-digit exponent); otherwise fixed-point. C++ std::to_chars
// shortest mode picks whichever string is shorter, so e.g. 100000 differs ("1e+05"
// in to_chars, "100000" in Go) — replicate Go's threshold here.
std::string go_format_g(double d) {
    if (std::isnan(d)) return "NaN";
    if (std::isinf(d)) return d < 0 ? "-Inf" : "+Inf";
    if (d == 0.0) return std::signbit(d) ? "-0" : "0";

    char buf[64];
    auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d, std::chars_format::scientific);
    std::string s(buf, ptr);

    std::size_t i = 0;
    bool neg = false;
    if (s[i] == '-') { neg = true; ++i; }
    std::string mantissa;
    while (i < s.size() && s[i] != 'e' && s[i] != 'E') {
        if (s[i] != '.') mantissa.push_back(s[i]);
        ++i;
    }
    ++i;  // skip 'e'
    bool exp_neg = false;
    if (i < s.size() && (s[i] == '+' || s[i] == '-')) {
        exp_neg = (s[i] == '-');
        ++i;
    }
    int exp_val = 0;
    while (i < s.size()) { exp_val = exp_val * 10 + (s[i] - '0'); ++i; }
    if (exp_neg) exp_val = -exp_val;

    int nd = static_cast<int>(mantissa.size());
    std::string result;
    if (neg) result += '-';

    if (exp_val < -4 || exp_val >= 6) {
        result += mantissa[0];
        if (nd > 1) {
            result += '.';
            result.append(mantissa, 1, std::string::npos);
        }
        result += 'e';
        int e = exp_val;
        if (e >= 0) result += '+';
        else { result += '-'; e = -e; }
        if (e < 10) result += '0';
        result += std::to_string(e);
    } else {
        int dp_pos = exp_val + 1;
        if (dp_pos <= 0) {
            result += "0.";
            for (int k = 0; k < -dp_pos; ++k) result += '0';
            result += mantissa;
        } else if (dp_pos >= nd) {
            result += mantissa;
            for (int k = 0; k < dp_pos - nd; ++k) result += '0';
        } else {
            result.append(mantissa, 0, dp_pos);
            result += '.';
            result.append(mantissa, dp_pos, std::string::npos);
        }
    }
    return result;
}

std::string sprint_value(const JsonValue& v) {
    if (v.is_null()) return "<nil>";
    if (v.is_bool()) return v.as_bool() ? "true" : "false";
    if (v.is_number()) return go_format_g(v.as_number());
    if (v.is_string()) return v.as_string();
    return "<complex>";
}

void run_filter_condition(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    const auto& params = op.params.as_object();
    auto val_it = params.find("value");
    if (val_it == params.end()) throw ExecutionError(op.name, "filter_condition: missing required param 'value'");
    const std::string target = sprint_value(val_it->second);
    const auto& field = op.metadata.item_input.at(0);
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        JsonValue fv = frame.item(i, field);
        if (sprint_value(fv) == target) out.remove_item(static_cast<int>(i));
    }
}

void run_filter_paginate(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    int n = static_cast<int>(frame.item_count());
    if (n == 0) return;
    auto to_int = [](const JsonValue& v) -> int {
        if (v.is_number()) return static_cast<int>(v.as_number());
        return 0;
    };
    int page = to_int(frame.common(op.metadata.common_input.at(0)));
    int size = to_int(frame.common(op.metadata.common_input.at(1)));
    if (size <= 0) size = 10;
    if (page < 0) page = 0;
    int start = page * size;
    int end = start + size;
    if (end > n) end = n;
    for (int i = 0; i < n; ++i) {
        if (i < start || i >= end) out.remove_item(i);
    }
}

// dedup_key encodes a JsonValue with a type tag so values of different runtime
// types never collapse, matching pine-go's `map[any]struct{}` semantics where
// interface equality requires identical dynamic type + value (so 1.0 and "1"
// are distinct keys). Without the tag, sprint_value(1.0) == sprint_value("1")
// == "1" would erroneously merge them.
std::string dedup_key(const JsonValue& v) {
    if (v.is_null()) return "N:";
    if (v.is_bool()) return v.as_bool() ? "B:1" : "B:0";
    if (v.is_number()) return "F:" + go_format_g(v.as_number());
    if (v.is_string()) return "S:" + v.as_string();
    return "O:" + sprint_value(v);
}

void run_merge_dedup(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    const auto& field = op.metadata.item_input.at(0);
    std::vector<std::string> seen;
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        JsonValue fv = frame.item(i, field);
        std::string key = dedup_key(fv);
        bool dup = false;
        for (const auto& s : seen) { if (s == key) { dup = true; break; } }
        if (dup) {
            out.remove_item(static_cast<int>(i));
        } else {
            seen.push_back(key);
        }
    }
}

void run_observe_log(const Frame& /*frame*/, const OperatorConfig& /*op*/, OperatorOutput& /*out*/) {
}

void run_transform_normalize(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    if (frame.item_count() == 0) return;
    const auto& field = op.metadata.item_input.at(0);
    const auto& out_field = op.metadata.item_output.at(0);
    std::vector<double> vals;
    vals.reserve(frame.item_count());
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        try {
            vals.push_back(to_double(require_item(frame, op, i, field)));
        } catch (const OperatorError& err) {
            throw ExecutionError(op.name, "transform_normalize: item[" + std::to_string(i) + "]." + field + ": " + err.what());
        }
    }
    double minv = *std::min_element(vals.begin(), vals.end());
    double maxv = *std::max_element(vals.begin(), vals.end());
    double rng = maxv - minv;
    for (std::size_t i = 0; i < vals.size(); ++i) {
        double norm = (rng == 0.0) ? 0.0 : (vals[i] - minv) / rng;
        out.set_item(static_cast<int>(i), out_field, JsonValue(norm));
    }
}

uint64_t fnv64a(const std::string& s) {
    uint64_t hash = 14695981039346656037ULL;
    for (unsigned char c : s) {
        hash ^= c;
        hash *= 1099511628211ULL;
    }
    return hash;
}

std::string any_to_string(const JsonValue& v) {
    if (v.is_null()) return "";
    if (v.is_string()) return v.as_string();
    if (v.is_number()) return go_format_g(v.as_number());
    if (v.is_bool()) return v.as_bool() ? "true" : "false";
    return "";
}

uint64_t parse_uint64(const std::string& s) {
    uint64_t result = 0;
    std::from_chars(s.data(), s.data() + s.size(), result);
    return result;
}

void run_reorder_shuffle_by_salt(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    if (frame.item_count() == 0) return;
    std::string salt;
    for (std::size_t i = 0; i < op.metadata.common_input.size(); ++i) {
        if (i > 0) salt += '|';
        salt += any_to_string(frame.common(op.metadata.common_input[i]));
    }
    salt += '|';
    const auto& item_field = op.metadata.item_input.at(0);
    struct Ranked { std::size_t idx; double r; uint64_t id; };
    std::vector<Ranked> ranked;
    ranked.reserve(frame.item_count());
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        std::string item_val = any_to_string(frame.item(i, item_field));
        uint64_t h = fnv64a(salt + item_val);
        double r = static_cast<double>(h) / (static_cast<double>(UINT64_MAX) + 1.0);
        ranked.push_back({i, r, parse_uint64(item_val)});
    }
    std::stable_sort(ranked.begin(), ranked.end(), [](const Ranked& a, const Ranked& b) {
        if (a.r != b.r) return a.r < b.r;
        return a.id < b.id;
    });
    std::vector<int> order;
    order.reserve(ranked.size());
    for (const auto& r : ranked) order.push_back(static_cast<int>(r.idx));
    out.set_item_order(std::move(order));
}

void run_filter_truncate(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    int top_n = static_cast<int>(op.params.as_object().at("top_n").as_number());
    if (top_n < 0) throw ExecutionError(op.name, "filter_truncate: top_n must be non-negative");
    int n = static_cast<int>(frame.item_count());
    for (int i = top_n; i < n; ++i) out.remove_item(i);
}

void run_recall_static(const Frame& /*frame*/, const OperatorConfig& op, OperatorOutput& out) {
    for (const auto& item : op.params.as_object().at("items").as_array()) {
        Row row;
        for (const auto& [key, value] : item.as_object()) row[key] = value;
        out.add_item(std::move(row));
    }
}

void run_recall_resource(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    if (!frame.resources())
        throw ExecutionError(op.name, "recall_resource: no resource provider in context");
    const auto& params = op.params.as_object();
    auto rn_it = params.find("resource_name");
    if (rn_it == params.end() || !rn_it->second.is_string())
        throw ExecutionError(op.name, "recall_resource: missing resource_name");
    const std::string& resource_name = rn_it->second.as_string();
    auto res_it = frame.resources()->find(resource_name);
    if (res_it == frame.resources()->end())
        throw ExecutionError(op.name, "recall_resource: resource \"" + resource_name + "\" not found");
    const auto& resource = res_it->second;
    if (!resource.is_array())
        throw ExecutionError(op.name, "recall_resource: resource \"" + resource_name + "\" is not an array, want []map[string]any");
    for (std::size_t i = 0; i < resource.as_array().size(); ++i) {
        const auto& elem = resource.as_array()[i];
        if (!elem.is_object())
            throw ExecutionError(op.name, "recall_resource: items[" + std::to_string(i) + "] is not an object, want map[string]any");
        Row row;
        for (const auto& [key, value] : elem.as_object()) row[key] = value;
        out.add_item(std::move(row));
    }
}

void run_transform_resource_lookup(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    if (!frame.resources())
        throw ExecutionError(op.name, "transform_resource_lookup: no resource provider in context");
    const auto& params = op.params.as_object();
    auto rn_it = params.find("resource_name");
    if (rn_it == params.end() || !rn_it->second.is_string())
        throw ExecutionError(op.name, "transform_resource_lookup: missing resource_name");
    const std::string& resource_name = rn_it->second.as_string();
    auto res_it = frame.resources()->find(resource_name);
    if (res_it == frame.resources()->end())
        throw ExecutionError(op.name, "transform_resource_lookup: resource \"" + resource_name + "\" not found");
    const auto& resource = res_it->second;
    if (!resource.is_object())
        throw ExecutionError(op.name, "transform_resource_lookup: resource \"" + resource_name + "\" is not an object, want map[string]any");
    const auto& table = resource.as_object();

    auto lk_it = params.find("lookup_key");
    if (lk_it == params.end() || !lk_it->second.is_string())
        throw ExecutionError(op.name, "transform_resource_lookup: missing lookup_key");
    const std::string& lookup_key = lk_it->second.as_string();

    auto of_it = params.find("output_field");
    if (of_it == params.end() || !of_it->second.is_string())
        throw ExecutionError(op.name, "transform_resource_lookup: missing output_field");
    const std::string& output_field = of_it->second.as_string();

    auto dv_it = params.find("default_value");
    bool has_default = (dv_it != params.end());

    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        JsonValue field_val = frame.item(i, lookup_key);
        if (field_val.is_null()) {
            if (has_default) out.set_item(static_cast<int>(i), output_field, dv_it->second);
            continue;
        }
        std::string key;
        if (field_val.is_string()) {
            key = field_val.as_string();
        } else if (field_val.is_number()) {
            double d = field_val.as_number();
            if (d == static_cast<double>(static_cast<int64_t>(d))) {
                key = std::to_string(static_cast<int64_t>(d));
            } else {
                char buf[32];
                auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d);
                key = std::string(buf, ptr);
            }
        } else {
            key = sprint_value(field_val);
        }
        auto val_it = table.find(key);
        if (val_it != table.end()) {
            out.set_item(static_cast<int>(i), output_field, val_it->second);
        } else if (has_default) {
            out.set_item(static_cast<int>(i), output_field, dv_it->second);
        }
    }
}

std::string build_key_suffix(const Frame& frame, const std::vector<std::string>& fields) {
    if (fields.empty()) return "";
    std::string result;
    for (std::size_t i = 0; i < fields.size(); ++i) {
        if (i > 0) result += ':';
        result += sprint_value(frame.common(fields[i]));
    }
    return result;
}

struct RedisParams {
    std::string host;
    int port = 6379;
    std::string password;
    int db = 0;
    std::string key_prefix;
    std::string data_type = "string";
    int ttl = 0;
    bool fail_on_error = false;
};

RedisParams parse_redis_params(const OperatorConfig& op) {
    RedisParams rp;
    const auto& params = op.params.as_object();
    auto addr_it = params.find("redis_addr");
    if (addr_it != params.end() && addr_it->second.is_string()) {
        const auto& addr = addr_it->second.as_string();
        auto colon = addr.rfind(':');
        if (colon != std::string::npos) {
            rp.host = addr.substr(0, colon);
            rp.port = std::stoi(addr.substr(colon + 1));
        } else {
            rp.host = addr;
        }
    }
    if (auto it = params.find("redis_password"); it != params.end() && it->second.is_string())
        rp.password = it->second.as_string();
    if (auto it = params.find("redis_db"); it != params.end() && it->second.is_number())
        rp.db = static_cast<int>(it->second.as_number());
    if (auto it = params.find("key_prefix"); it != params.end() && it->second.is_string())
        rp.key_prefix = it->second.as_string();
    if (auto it = params.find("data_type"); it != params.end() && it->second.is_string())
        rp.data_type = it->second.as_string();
    if (auto it = params.find("ttl"); it != params.end() && it->second.is_number())
        rp.ttl = static_cast<int>(it->second.as_number());
    if (auto it = params.find("fail_on_error"); it != params.end() && it->second.is_bool())
        rp.fail_on_error = it->second.as_bool();
    return rp;
}

std::vector<std::string> json_to_string_slice(const JsonValue& v) {
    std::vector<std::string> out;
    if (!v.is_array()) return out;
    for (const auto& item : v.as_array()) {
        out.push_back(sprint_value(item));
    }
    return out;
}

void run_transform_redis_set(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    auto rp = parse_redis_params(op);
    if (rp.host.empty()) return;

    int n = static_cast<int>(op.metadata.common_input.size());
    if (n < 2)
        throw ExecutionError(op.name, "transform_redis_set: common_input must have at least 2 fields (key fields + value field)");

    std::vector<std::string> key_fields(op.metadata.common_input.begin(), op.metadata.common_input.begin() + (n - 1));
    std::string key = rp.key_prefix + build_key_suffix(frame, key_fields);

    JsonValue value = frame.common(op.metadata.common_input.back());

    std::unique_ptr<redis::Client> client;
    try {
        client = std::make_unique<redis::Client>(rp.host, rp.port, rp.password, rp.db);
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError(op.name, "transform_redis_set: write key " + key + ": " + e.what());
        out.set_warning("operator \"" + op.name + "\": transform_redis_set: write key " + key + ": " + std::string(e.what()));
        return;
    }
    if (!client->connected()) {
        if (rp.fail_on_error)
            throw ExecutionError(op.name, "transform_redis_set: write key " + key + ": connection failed");
        out.set_warning("operator \"" + op.name + "\": transform_redis_set: write key " + key + ": connection failed");
        return;
    }

    try {
        if (rp.data_type == "string") {
            if (!value.is_string()) return;
            client->set(key, value.as_string(), rp.ttl);
        } else if (rp.data_type == "set") {
            auto members = json_to_string_slice(value);
            if (members.empty()) return;
            client->del(key);
            client->sadd(key, members);
            if (rp.ttl > 0) client->expire(key, rp.ttl);
        } else if (rp.data_type == "list") {
            auto members = json_to_string_slice(value);
            if (members.empty()) return;
            client->del(key);
            client->rpush(key, members);
            if (rp.ttl > 0) client->expire(key, rp.ttl);
        } else {
            throw ExecutionError(op.name, "transform_redis_set: unsupported data_type \"" + rp.data_type + "\"");
        }
    } catch (const ExecutionError&) {
        throw;
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError(op.name, "transform_redis_set: write key " + key + ": " + e.what());
        out.set_warning("operator \"" + op.name + "\": transform_redis_set: write key " + key + ": " + std::string(e.what()));
    }
}

void run_transform_redis_get(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    auto rp = parse_redis_params(op);
    const auto& result_field = op.metadata.common_output.at(0);
    const auto& cache_hit_field = op.metadata.common_output.at(1);

    if (rp.host.empty()) {
        out.set_common(cache_hit_field, JsonValue(false));
        return;
    }

    std::string key = rp.key_prefix + build_key_suffix(frame, op.metadata.common_input);

    std::unique_ptr<redis::Client> client;
    try {
        client = std::make_unique<redis::Client>(rp.host, rp.port, rp.password, rp.db);
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError(op.name, "transform_redis_get: " + std::string(e.what()));
        out.set_warning("operator \"" + op.name + "\": transform_redis_get: Get(" + key + "): " + std::string(e.what()));
        out.set_common(cache_hit_field, JsonValue(false));
        return;
    }
    if (!client->connected()) {
        if (rp.fail_on_error)
            throw ExecutionError(op.name, "transform_redis_get: connection failed");
        out.set_warning("operator \"" + op.name + "\": transform_redis_get: Get(" + key + "): connection failed");
        out.set_common(cache_hit_field, JsonValue(false));
        return;
    }

    try {
        if (rp.data_type == "string") {
            auto val = client->get(key);
            if (val && !val->empty()) {
                out.set_common(result_field, JsonValue(*val));
                out.set_common(cache_hit_field, JsonValue(true));
            } else {
                out.set_common(cache_hit_field, JsonValue(false));
            }
        } else if (rp.data_type == "set") {
            auto members = client->smembers(key);
            if (!members.empty()) {
                JsonValue::array_t arr;
                for (auto& m : members) arr.push_back(JsonValue(std::move(m)));
                out.set_common(result_field, JsonValue(std::move(arr)));
                out.set_common(cache_hit_field, JsonValue(true));
            } else {
                out.set_common(cache_hit_field, JsonValue(false));
            }
        } else if (rp.data_type == "list") {
            auto vals = client->lrange(key, 0, -1);
            if (!vals.empty()) {
                JsonValue::array_t arr;
                for (auto& v : vals) arr.push_back(JsonValue(std::move(v)));
                out.set_common(result_field, JsonValue(std::move(arr)));
                out.set_common(cache_hit_field, JsonValue(true));
            } else {
                out.set_common(cache_hit_field, JsonValue(false));
            }
        } else {
            throw ExecutionError(op.name, "transform_redis_get: unsupported data_type \"" + rp.data_type + "\"");
        }
    } catch (const ExecutionError&) {
        throw;
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError(op.name, "transform_redis_get: " + std::string(e.what()));
        std::string cmd_name = (rp.data_type == "set") ? "SMembers" : (rp.data_type == "list") ? "LRange" : "Get";
        out.set_warning("operator \"" + op.name + "\": transform_redis_get: " + cmd_name + "(" + key + "): " + std::string(e.what()));
        out.set_common(cache_hit_field, JsonValue(false));
    }
}

void run_reorder_sort(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    if (frame.item_count() == 0) return;
    const std::string order = [&]() {
        const auto& obj = op.params.as_object();
        auto it = obj.find("order");
        return it == obj.end() ? std::string("desc") : it->second.as_string();
    }();
    if (op.metadata.item_input.empty()) throw ExecutionError(op.name, "reorder_sort requires item_input field");
    const std::string field = op.metadata.item_input.front();
    struct Keyed { double v; std::size_t idx; };
    std::vector<Keyed> keyed;
    keyed.reserve(frame.item_count());
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        try {
            keyed.push_back({to_double(require_item(frame, op, i, field)), i});
        } catch (const OperatorError& err) {
            throw ExecutionError(op.name, "reorder_sort: item[" + std::to_string(i) + "]." + field + ": " + err.what());
        }
    }
    if (order == "asc") {
        std::stable_sort(keyed.begin(), keyed.end(), [](const Keyed& a, const Keyed& b) { return a.v < b.v; });
    } else if (order == "desc") {
        std::stable_sort(keyed.begin(), keyed.end(), [](const Keyed& a, const Keyed& b) { return a.v > b.v; });
    } else {
        throw ExecutionError(op.name, "reorder_sort: unsupported order \"" + order + "\"");
    }
    std::vector<int> order_vec;
    order_vec.reserve(keyed.size());
    for (const auto& k : keyed) order_vec.push_back(static_cast<int>(k.idx));
    out.set_item_order(std::move(order_vec));
}

void run_transform_by_lua(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    const auto& params = op.params.as_object();
    auto script_it = params.find("lua_script");
    if (script_it == params.end() || !script_it->second.is_string())
        throw ExecutionError(op.name, "lua: exactly one of function_for_item or function_for_common must be set");

    auto fi_it = params.find("function_for_item");
    auto fc_it = params.find("function_for_common");
    std::string func_for_item = (fi_it != params.end() && fi_it->second.is_string()) ? fi_it->second.as_string() : "";
    std::string func_for_common = (fc_it != params.end() && fc_it->second.is_string()) ? fc_it->second.as_string() : "";

    if (func_for_item.empty() && func_for_common.empty())
        throw ExecutionError(op.name, "lua: exactly one of function_for_item or function_for_common must be set");
    if (!func_for_item.empty() && !func_for_common.empty())
        throw ExecutionError(op.name, "lua: cannot set both function_for_item and function_for_common");

    auto resolve_common = [&](const std::string& field) -> JsonValue {
        JsonValue v = frame.common(field);
        if (!v.is_null()) return v;
        auto def = op.common_defaults.find(field);
        if (def != op.common_defaults.end()) return def->second;
        return JsonValue();
    };
    auto resolve_item = [&](std::size_t idx, const std::string& field) -> JsonValue {
        JsonValue v = frame.item(idx, field);
        if (!v.is_null()) return v;
        auto def = op.item_defaults.find(field);
        if (def != op.item_defaults.end()) return def->second;
        return JsonValue();
    };

    lua::LuaVM vm;
    vm.load_script(script_it->second.as_string(), op.name);

    if (!func_for_item.empty()) {
        int nret = static_cast<int>(op.metadata.item_output.size());
        for (const auto& field : op.metadata.common_input)
            vm.set_global(field, resolve_common(field));
        for (std::size_t i = 0; i < frame.item_count(); ++i) {
            for (const auto& field : op.metadata.item_input)
                vm.set_global(field, resolve_item(i, field));
            auto results = vm.call_function(func_for_item, nret, op.name);
            for (int j = 0; j < nret; ++j)
                out.set_item(static_cast<int>(i), op.metadata.item_output[static_cast<std::size_t>(j)], results[static_cast<std::size_t>(j)]);
        }
    } else {
        int nret = static_cast<int>(op.metadata.common_output.size());
        for (const auto& field : op.metadata.common_input)
            vm.set_global(field, resolve_common(field));
        for (const auto& field : op.metadata.item_input) {
            std::vector<JsonValue> column;
            column.reserve(frame.item_count());
            for (std::size_t i = 0; i < frame.item_count(); ++i)
                column.push_back(resolve_item(i, field));
            vm.set_global_table(field, column);
        }
        auto results = vm.call_function(func_for_common, nret, op.name);
        for (int j = 0; j < nret; ++j)
            out.set_common(op.metadata.common_output[static_cast<std::size_t>(j)], results[static_cast<std::size_t>(j)]);
    }
}

Result project_result(const Frame& frame, const FlowContract& contract) {
    return frame.to_result(contract.common_output, contract.item_output);
}

void validate_request(const Request& request, const FlowContract& contract) {
    for (const auto& field : contract.common_input) if (!request.common.count(field)) throw ValidationError("missing required common input field \"" + field + "\"");
    for (std::size_t i = 0; i < request.items.size(); ++i) {
        for (const auto& field : contract.item_input) if (!request.items[i].count(field)) throw ValidationError("item[" + std::to_string(i) + "] missing required item input field \"" + field + "\"");
    }
}

// apply_output is now a member of ColumnFrame (frame.apply_output(out, op_name, is_recall)).
// See pine-cpp/src/dataframe/column_frame.{hpp,cpp} for the canonical
// five-stage application (common writes -> item writes -> removals ->
// reorder -> additions; recall ops stamp `_source = op_name`).

// snapshot_input builds the per-op input view that pine-go records as
// OpTrace.InputSnapshot when debug=true. Includes only declared input fields
// (filtered by skip), with defaults substituted for missing/null values.
// Items section omitted entirely when no item input field has any value.
JsonValue snapshot_input(const Frame& frame, const OperatorConfig& op) {
    JsonValue::object_t snap;
    std::set<std::string> skip_set(op.skip.begin(), op.skip.end());

    JsonValue::object_t common;
    for (const auto& field : op.metadata.common_input) {
        if (skip_set.count(field)) continue;
        JsonValue v = frame.common(field);
        if (!v.is_null()) {
            common[field] = v;
        } else if (auto def = op.common_defaults.find(field); def != op.common_defaults.end()) {
            common[field] = def->second;
        } else {
            common[field] = JsonValue();
        }
    }
    if (!common.empty()) snap["common"] = JsonValue(std::move(common));

    if (frame.item_count() > 0 && !op.metadata.item_input.empty()) {
        bool has_data = false;
        JsonValue::array_t items;
        items.reserve(frame.item_count());
        for (std::size_t i = 0; i < frame.item_count(); ++i) {
            JsonValue::object_t row;
            for (const auto& field : op.metadata.item_input) {
                JsonValue v = frame.item(i, field);
                if (!v.is_null()) {
                    row[field] = v;
                } else if (auto def = op.item_defaults.find(field); def != op.item_defaults.end()) {
                    row[field] = def->second;
                } else {
                    row[field] = JsonValue();
                }
            }
            if (!row.empty()) has_data = true;
            items.push_back(JsonValue(std::move(row)));
        }
        if (has_data) snap["items"] = JsonValue(std::move(items));
    }

    return JsonValue(std::move(snap));
}

// snapshot_output mirrors pine-go's snapshotOutput: serialize the
// OperatorOutput buffer into a stable JSON-friendly shape.
JsonValue snapshot_output(const OperatorOutput& out) {
    JsonValue::object_t snap;

    if (!out.common_writes().empty()) {
        JsonValue::object_t cw;
        for (const auto& [field, value] : out.common_writes()) cw[field] = value;
        snap["common_writes"] = JsonValue(std::move(cw));
    }

    if (!out.item_writes().empty()) {
        JsonValue::object_t iw;
        for (const auto& [idx, fields] : out.item_writes()) {
            JsonValue::object_t row;
            for (const auto& [field, value] : fields) row[field] = value;
            iw[std::to_string(idx)] = JsonValue(std::move(row));
        }
        snap["item_writes"] = JsonValue(std::move(iw));
    }

    if (!out.added_items().empty()) {
        JsonValue::array_t ai;
        ai.reserve(out.added_items().size());
        for (const auto& row : out.added_items()) {
            JsonValue::object_t obj;
            for (const auto& [field, value] : row) obj[field] = value;
            ai.push_back(JsonValue(std::move(obj)));
        }
        snap["added_items"] = JsonValue(std::move(ai));
    }

    if (!out.removed_items().empty()) {
        JsonValue::array_t ri;
        ri.reserve(out.removed_items().size());
        for (int idx : out.removed_items()) ri.push_back(JsonValue(static_cast<double>(idx)));
        snap["removed_items"] = JsonValue(std::move(ri));
    }

    return JsonValue(std::move(snap));
}

int64_t now_us() {
    using namespace std::chrono;
    return duration_cast<microseconds>(system_clock::now().time_since_epoch()).count();
}

}  // namespace

Engine::Engine(Config config) : Engine(std::move(config), EngineOptions{}) {}

Engine::Engine(Config config, EngineOptions options) : config_(std::move(config)) {
    bool global_debug = options.debug.has_value() ? *options.debug : config_.debug;
    if (global_debug) {
        for (auto& [_, op] : config_.operators) op.debug = true;
    }
    expanded_ = expand_operator_sequence_with_subflows(config_);
    graph_ = build_dag(config_, expanded_);
}

Engine Engine::from_file(const std::string& path) { return Engine(load_config_from_file(path)); }
Engine Engine::from_file(const std::string& path, EngineOptions options) {
    return Engine(load_config_from_file(path), std::move(options));
}

namespace {

void dispatch_operator(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    if (op.type_name == "transform_copy") run_transform_copy(frame, op, out);
    else if (op.type_name == "transform_dispatch") run_transform_dispatch(frame, op, out);
    else if (op.type_name == "transform_size") run_transform_size(frame, op, out);
    else if (op.type_name == "transform_normalize") run_transform_normalize(frame, op, out);
    else if (op.type_name == "transform_by_lua") run_transform_by_lua(frame, op, out);
    else if (op.type_name == "filter_truncate") run_filter_truncate(frame, op, out);
    else if (op.type_name == "filter_condition") run_filter_condition(frame, op, out);
    else if (op.type_name == "filter_paginate") run_filter_paginate(frame, op, out);
    else if (op.type_name == "recall_static") run_recall_static(frame, op, out);
    else if (op.type_name == "recall_resource") run_recall_resource(frame, op, out);
    else if (op.type_name == "transform_resource_lookup") run_transform_resource_lookup(frame, op, out);
    else if (op.type_name == "transform_redis_set") run_transform_redis_set(frame, op, out);
    else if (op.type_name == "transform_redis_get") run_transform_redis_get(frame, op, out);
    else if (op.type_name == "reorder_sort") run_reorder_sort(frame, op, out);
    else if (op.type_name == "reorder_shuffle_by_salt") run_reorder_shuffle_by_salt(frame, op, out);
    else if (op.type_name == "merge_dedup") run_merge_dedup(frame, op, out);
    else if (op.type_name == "observe_log") run_observe_log(frame, op, out);
    else throw RegistryError("operator \"" + op.name + "\": operator type not registered: \"" + op.type_name + "\"");
}

void validate_item_inputs(const Frame& frame, const OperatorConfig& op) {
    for (std::size_t i = 0; i < frame.item_count(); ++i) {
        for (const auto& field : op.metadata.item_input) {
            if (op.item_defaults.count(field)) continue;
            JsonValue v = frame.item(i, field);
            if (v.is_null()) {
                throw ExecutionError(op.name, "required field \"" + field + "\" is nil on item[" + std::to_string(i) + "]");
            }
        }
    }
}

// dispatch_with_recovery runs dispatch_operator and converts any non-pine::Error
// exception into a PanicError carrying the operator name. Pine typed errors
// (ExecutionError, RegistryError, etc.) propagate unchanged because they
// already encode the operator context in their message.
void dispatch_with_recovery(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    try {
        dispatch_operator(frame, op, out);
    } catch (const Error&) {
        throw;
    } catch (const std::exception& e) {
        throw PanicError(op.name, e.what());
    } catch (...) {
        throw PanicError(op.name, "unknown exception");
    }
}

// merge_shard_output merges a shard's OperatorOutput into the master output,
// applying `offset` to item-index references. Parallel ops are constrained to
// transforms (no added_items, no item_order, empty common_output), so only
// item_writes, removed_items, and warnings need merging.
void merge_shard_output(OperatorOutput& dst, const OperatorOutput& src, int offset) {
    for (const auto& [idx, fields] : src.item_writes()) {
        for (const auto& [field, value] : fields) {
            dst.set_item(idx + offset, field, value);
        }
    }
    for (int idx : src.removed_items()) {
        dst.remove_item(idx + offset);
    }
    if (src.has_warning()) {
        dst.set_warning(src.warning());
    }
}

// parallel_execute shards frame.items across op.data_parallel workers, executes
// the operator concurrently on each shard, and merges shard OperatorOutputs
// back into `out` with index offsets. Mirrors pine-go's runtime/parallel.go.
//
// Preconditions (enforced by config validation):
//   - op.operator_type == "transform"
//   - op.metadata.common_output is empty
//   - op.type_name is in the ConcurrentSafe set
// Therefore shards only emit item_writes / removed_items / warnings.
void parallel_execute(const Frame& frame, const OperatorConfig& op, OperatorOutput& out) {
    int total = static_cast<int>(frame.item_count());
    int n = op.data_parallel;
    if (n <= 1 || total == 0) {
        dispatch_with_recovery(frame, op, out);
        return;
    }
    if (n > total) n = total;

    int base = total / n;
    int rem = total % n;

    // Materialize the source frame's common into a plain map for shard
    // construction. The original ColumnFrame's resources pointer is shared.
    std::map<std::string, JsonValue> common_snapshot;
    for (const auto& f : frame.common_fields()) {
        common_snapshot[f] = frame.common(f);
    }

    std::vector<std::unique_ptr<Frame>> shards;
    shards.reserve(static_cast<std::size_t>(n));
    std::vector<OperatorOutput> shard_outs(static_cast<std::size_t>(n));
    std::vector<int> offsets(static_cast<std::size_t>(n));
    int cursor = 0;
    for (int i = 0; i < n; ++i) {
        int size = base + (i < rem ? 1 : 0);
        std::vector<std::map<std::string, JsonValue>> shard_items;
        shard_items.reserve(static_cast<std::size_t>(size));
        for (int j = 0; j < size; ++j) {
            auto obj = frame.item_object(static_cast<std::size_t>(cursor + j));
            shard_items.emplace_back(obj.begin(), obj.end());
        }
        auto shard = std::make_unique<Frame>(common_snapshot, std::move(shard_items));
        shard->set_resources(frame.resources());
        shards.push_back(std::move(shard));
        offsets[static_cast<std::size_t>(i)] = cursor;
        cursor += size;
    }

    std::mutex err_mu;
    std::exception_ptr first_err;

    std::vector<std::thread> threads;
    threads.reserve(static_cast<std::size_t>(n));
    for (int i = 0; i < n; ++i) {
        threads.emplace_back([&shards, &shard_outs, &op, &err_mu, &first_err, i]() {
            try {
                dispatch_with_recovery(*shards[static_cast<std::size_t>(i)], op,
                                       shard_outs[static_cast<std::size_t>(i)]);
            } catch (...) {
                std::lock_guard<std::mutex> lk(err_mu);
                if (!first_err) first_err = std::current_exception();
            }
        });
    }
    for (auto& t : threads) t.join();

    if (first_err) std::rethrow_exception(first_err);

    for (int i = 0; i < n; ++i) {
        merge_shard_output(out, shard_outs[static_cast<std::size_t>(i)],
                           offsets[static_cast<std::size_t>(i)]);
    }
}

// run_dag executes the DAG concurrently: each node runs on its own thread,
// waits on predecessor completion via shared_futures, and accesses Frame
// under a shared_mutex (shared lock for reads, unique lock for apply_output).
// On the first fatal exception, all unstarted nodes observe `cancelled` and
// bail out; the captured exception is rethrown by the caller.
// Mirrors pine-go internal/runtime/scheduler.go (per-node goroutines, done
// channels, fatalOnce + context cancel).
std::vector<OpTrace> run_dag(const Config& config,
                             const Graph& graph,
                             Frame& frame,
                             bool collect_traces) {
    const std::size_t n = graph.nodes.size();

    std::vector<std::promise<void>> promises(n);
    std::vector<std::shared_future<void>> futures;
    futures.reserve(n);
    for (auto& p : promises) futures.push_back(p.get_future().share());

    std::vector<OpTrace> traces;
    if (collect_traces) traces.assign(n, OpTrace{});

    std::shared_mutex frame_mu;
    std::mutex fatal_mu;
    std::exception_ptr fatal_err;
    std::atomic<bool> cancelled{false};

    auto fail = [&](std::exception_ptr e) {
        std::lock_guard<std::mutex> lk(fatal_mu);
        if (!fatal_err) {
            fatal_err = e;
            cancelled.store(true, std::memory_order_release);
        }
    };

    std::vector<std::thread> threads;
    threads.reserve(n);
    for (std::size_t i = 0; i < n; ++i) {
        threads.emplace_back([&, i]() {
            // RAII: always notify successors, even on early return / exception.
            struct Notifier {
                std::promise<void>& p;
                ~Notifier() { try { p.set_value(); } catch (...) {} }
            } notifier{promises[i]};
            (void)notifier;

            const auto& node = graph.nodes[i];
            const auto& op = config.operators.at(node.name);

            for (int pred : node.preds) {
                futures[static_cast<std::size_t>(pred)].wait();
            }
            if (cancelled.load(std::memory_order_acquire)) return;

            OpTrace trace;
            if (collect_traces) {
                trace.name = op.name;
                trace.start_time_us = now_us();
            }
            auto start = std::chrono::steady_clock::now();

            try {
                bool skip;
                {
                    std::shared_lock<std::shared_mutex> lk(frame_mu);
                    skip = should_skip(frame, op);
                }
                if (skip) {
                    if (collect_traces) {
                        auto end = std::chrono::steady_clock::now();
                        trace.duration_us = std::chrono::duration_cast<std::chrono::microseconds>(end - start).count();
                        trace.skipped = true;
                        traces[i] = std::move(trace);
                    }
                    return;
                }

                OperatorOutput out;
                {
                    std::shared_lock<std::shared_mutex> lk(frame_mu);
                    validate_item_inputs(frame, op);
                    if (collect_traces && op.debug) {
                        trace.input_snapshot = snapshot_input(frame, op);
                        trace.has_input_snapshot = true;
                    }
                    parallel_execute(frame, op, out);
                }
                if (collect_traces && op.debug) {
                    trace.output_snapshot = snapshot_output(out);
                    trace.has_output_snapshot = true;
                }
                {
                    std::unique_lock<std::shared_mutex> lk(frame_mu);
                    frame.apply_output(out, op.name, op.operator_type == "recall");
                }
                if (collect_traces) {
                    auto end = std::chrono::steady_clock::now();
                    trace.duration_us = std::chrono::duration_cast<std::chrono::microseconds>(end - start).count();
                    traces[i] = std::move(trace);
                }
            } catch (...) {
                fail(std::current_exception());
            }
        });
    }

    for (auto& t : threads) t.join();

    if (fatal_err) std::rethrow_exception(fatal_err);

    return traces;
}

}  // namespace

Result Engine::execute(const Request& request) const {
    static const std::map<std::string, JsonValue> empty_resources;
    return execute(request, empty_resources);
}

Result Engine::execute(const Request& request, const std::map<std::string, JsonValue>& resources) const {
    validate_request(request, config_.flow_contract);
    Frame frame(request.common, request.items);
    frame.set_resources(&resources);
    run_dag(config_, graph_, frame, /*collect_traces=*/false);
    return project_result(frame, config_.flow_contract);
}

TracedResult Engine::execute_traced(const Request& request, const std::map<std::string, JsonValue>& resources) const {
    TracedResult traced;
    validate_request(request, config_.flow_contract);
    Frame frame(request.common, request.items);
    frame.set_resources(&resources);
    traced.trace = run_dag(config_, graph_, frame, /*collect_traces=*/true);
    traced.result = project_result(frame, config_.flow_contract);
    traced.warnings = frame.take_warnings();
    return traced;
}

std::string Engine::render_dag(const std::string& format, int collapse) const {
    if (format == "dot") return collapse > 0 ? render_collapsed_dot(graph_, collapse) : render_dot(graph_);
    if (format == "mermaid") return collapse > 0 ? render_collapsed_mermaid(graph_, collapse) : render_mermaid(graph_);
    throw ValidationError("unsupported DAG format \"" + format + "\" (use \"dot\" or \"mermaid\")");
}

}  // namespace pine
