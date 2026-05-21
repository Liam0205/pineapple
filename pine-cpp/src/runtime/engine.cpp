#include "pine/pine.hpp"
#include "lua/lua_bridge.hpp"
#include "redis/redis_client.hpp"

#include <algorithm>
#include <charconv>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <map>
#include <memory>
#include <set>
#include <sstream>

namespace pine {
namespace {

using Row = std::map<std::string, JsonValue>;

struct Frame {
    std::map<std::string, JsonValue> common;
    std::vector<Row> items;
    const std::map<std::string, JsonValue>* resources = nullptr;
};

JsonValue require_common(const Frame& frame, const OperatorConfig& op, const std::string& field) {
    if (auto it = frame.common.find(field); it != frame.common.end() && !it->second.is_null()) return it->second;
    if (auto def = op.common_defaults.find(field); def != op.common_defaults.end()) return def->second;
    throw ExecutionError("operator \"" + op.name + "\": required field \"" + field + "\" is nil in common");
}

JsonValue require_item(const Frame& frame, const OperatorConfig& op, std::size_t index, const std::string& field) {
    if (auto it = frame.items[index].find(field); it != frame.items[index].end() && !it->second.is_null()) return it->second;
    if (auto def = op.item_defaults.find(field); def != op.item_defaults.end()) return def->second;
    throw ExecutionError("operator \"" + op.name + "\": required field \"" + field + "\" is nil on item[" + std::to_string(index) + "]");
}

bool should_skip(const Frame& frame, const OperatorConfig& op) {
    for (const auto& field : op.skip) {
        if (auto it = frame.common.find(field); it != frame.common.end() && it->second.truthy()) return true;
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

void run_transform_copy(Frame& frame, const OperatorConfig& op) {
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
            for (auto& item : frame.items) item[dst] = value;
        }
    } else if (direction == "common_to_common") {
        for (std::size_t i = 0; i < op.metadata.common_input.size(); ++i) {
            JsonValue value = require_common(frame, op, op.metadata.common_input[i]);
            frame.common[op.metadata.common_output.at(i)] = value;
        }
    } else if (direction == "item_to_item") {
        for (std::size_t i = 0; i < op.metadata.item_input.size(); ++i) {
            const auto& src = op.metadata.item_input[i];
            const auto& dst = op.metadata.item_output.at(i);
            for (std::size_t j = 0; j < frame.items.size(); ++j) {
                frame.items[j][dst] = require_item(frame, op, j, src);
            }
        }
    } else if (direction == "item_to_common") {
        for (std::size_t i = 0; i < op.metadata.item_input.size(); ++i) {
            const auto& src = op.metadata.item_input[i];
            JsonValue::array_t vals;
            for (std::size_t j = 0; j < frame.items.size(); ++j) {
                auto it = frame.items[j].find(src);
                vals.push_back(it != frame.items[j].end() ? it->second : JsonValue());
            }
            frame.common[op.metadata.common_output.at(i)] = JsonValue(vals);
        }
    } else {
        throw ExecutionError("operator \"" + op.name + "\": transform_copy: unsupported direction \"" + direction + "\"");
    }
}

void run_transform_dispatch(Frame& frame, const OperatorConfig& op) {
    const auto& src = op.metadata.common_input.at(0);
    const auto& dst = op.metadata.item_output.at(0);
    JsonValue val = require_common(frame, op, src);
    for (auto& item : frame.items) item[dst] = val;
}

void run_transform_size(Frame& frame, const OperatorConfig& op) {
    frame.common[op.metadata.common_output.at(0)] = JsonValue(static_cast<double>(frame.items.size()));
}

std::string sprint_value(const JsonValue& v) {
    if (v.is_null()) return "<nil>";
    if (v.is_bool()) return v.as_bool() ? "true" : "false";
    if (v.is_number()) {
        double d = v.as_number();
        char buf[32];
        auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d);
        return std::string(buf, ptr);
    }
    if (v.is_string()) return v.as_string();
    return "<complex>";
}

void run_filter_condition(Frame& frame, const OperatorConfig& op) {
    const auto& params = op.params.as_object();
    auto val_it = params.find("value");
    if (val_it == params.end()) throw ExecutionError("operator \"" + op.name + "\": filter_condition: missing required param 'value'");
    const std::string target = sprint_value(val_it->second);
    const auto& field = op.metadata.item_input.at(0);
    std::vector<Row> kept;
    for (auto& item : frame.items) {
        auto it = item.find(field);
        JsonValue fv = (it != item.end()) ? it->second : JsonValue();
        if (sprint_value(fv) != target) kept.push_back(std::move(item));
    }
    frame.items = std::move(kept);
}

void run_filter_paginate(Frame& frame, const OperatorConfig& op) {
    int n = static_cast<int>(frame.items.size());
    if (n == 0) return;
    auto to_int = [](const JsonValue& v) -> int {
        if (v.is_number()) return static_cast<int>(v.as_number());
        return 0;
    };
    auto get_common = [&](const std::string& f) -> JsonValue {
        auto it = frame.common.find(f);
        return (it != frame.common.end()) ? it->second : JsonValue();
    };
    int page = to_int(get_common(op.metadata.common_input.at(0)));
    int size = to_int(get_common(op.metadata.common_input.at(1)));
    if (size <= 0) size = 10;
    if (page < 0) page = 0;
    int start = page * size;
    int end = start + size;
    if (end > n) end = n;
    std::vector<Row> kept;
    for (int i = 0; i < n; ++i) {
        if (i >= start && i < end) kept.push_back(std::move(frame.items[static_cast<std::size_t>(i)]));
    }
    frame.items = std::move(kept);
}

void run_merge_dedup(Frame& frame, const OperatorConfig& op) {
    const auto& field = op.metadata.item_input.at(0);
    std::vector<std::string> seen;
    std::vector<Row> kept;
    for (auto& item : frame.items) {
        auto it = item.find(field);
        std::string key = (it != item.end()) ? sprint_value(it->second) : "<nil>";
        bool dup = false;
        for (const auto& s : seen) { if (s == key) { dup = true; break; } }
        if (!dup) {
            seen.push_back(key);
            kept.push_back(std::move(item));
        }
    }
    frame.items = std::move(kept);
}

void run_observe_log(const Frame& /*frame*/, const OperatorConfig& /*op*/) {
}

void run_transform_normalize(Frame& frame, const OperatorConfig& op) {
    if (frame.items.empty()) return;
    const auto& field = op.metadata.item_input.at(0);
    const auto& out_field = op.metadata.item_output.at(0);
    std::vector<double> vals;
    vals.reserve(frame.items.size());
    for (std::size_t i = 0; i < frame.items.size(); ++i) {
        try {
            vals.push_back(to_double(require_item(frame, op, i, field)));
        } catch (const OperatorError& err) {
            throw ExecutionError("operator \"" + op.name + "\": transform_normalize: item[" + std::to_string(i) + "]." + field + ": " + err.what());
        }
    }
    double minv = *std::min_element(vals.begin(), vals.end());
    double maxv = *std::max_element(vals.begin(), vals.end());
    double rng = maxv - minv;
    for (std::size_t i = 0; i < vals.size(); ++i) {
        double norm = (rng == 0.0) ? 0.0 : (vals[i] - minv) / rng;
        frame.items[i][out_field] = JsonValue(norm);
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
    if (v.is_number()) {
        double d = v.as_number();
        char buf[32];
        auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d);
        return std::string(buf, ptr);
    }
    if (v.is_bool()) return v.as_bool() ? "true" : "false";
    return "";
}

uint64_t parse_uint64(const std::string& s) {
    uint64_t result = 0;
    std::from_chars(s.data(), s.data() + s.size(), result);
    return result;
}

void run_reorder_shuffle_by_salt(Frame& frame, const OperatorConfig& op) {
    if (frame.items.empty()) return;
    std::string salt;
    for (std::size_t i = 0; i < op.metadata.common_input.size(); ++i) {
        if (i > 0) salt += '|';
        auto it = frame.common.find(op.metadata.common_input[i]);
        salt += (it != frame.common.end()) ? any_to_string(it->second) : "";
    }
    salt += '|';
    const auto& item_field = op.metadata.item_input.at(0);
    struct Ranked { std::size_t idx; double r; uint64_t id; };
    std::vector<Ranked> ranked;
    ranked.reserve(frame.items.size());
    for (std::size_t i = 0; i < frame.items.size(); ++i) {
        auto it = frame.items[i].find(item_field);
        std::string item_val = (it != frame.items[i].end()) ? any_to_string(it->second) : "";
        uint64_t h = fnv64a(salt + item_val);
        double r = static_cast<double>(h) / (static_cast<double>(UINT64_MAX) + 1.0);
        ranked.push_back({i, r, parse_uint64(item_val)});
    }
    std::stable_sort(ranked.begin(), ranked.end(), [](const Ranked& a, const Ranked& b) {
        if (a.r != b.r) return a.r < b.r;
        return a.id < b.id;
    });
    std::vector<Row> reordered;
    reordered.reserve(frame.items.size());
    for (const auto& r : ranked) reordered.push_back(std::move(frame.items[r.idx]));
    frame.items = std::move(reordered);
}

void run_filter_truncate(Frame& frame, const OperatorConfig& op) {
    int top_n = static_cast<int>(op.params.as_object().at("top_n").as_number());
    if (top_n < 0) throw ExecutionError("operator \"" + op.name + "\": filter_truncate: top_n must be non-negative");
    if (static_cast<int>(frame.items.size()) > top_n) frame.items.resize(static_cast<std::size_t>(top_n));
}

void run_recall_static(Frame& frame, const OperatorConfig& op) {
    for (const auto& item : op.params.as_object().at("items").as_array()) {
        Row row;
        for (const auto& [key, value] : item.as_object()) row[key] = value;
        row["_source"] = JsonValue(op.name);
        frame.items.push_back(std::move(row));
    }
}

void run_recall_resource(Frame& frame, const OperatorConfig& op) {
    if (!frame.resources)
        throw ExecutionError("operator \"" + op.name + "\": recall_resource: no resource provider in context");
    const auto& params = op.params.as_object();
    auto rn_it = params.find("resource_name");
    if (rn_it == params.end() || !rn_it->second.is_string())
        throw ExecutionError("operator \"" + op.name + "\": recall_resource: missing resource_name");
    const std::string& resource_name = rn_it->second.as_string();
    auto res_it = frame.resources->find(resource_name);
    if (res_it == frame.resources->end())
        throw ExecutionError("operator \"" + op.name + "\": recall_resource: resource \"" + resource_name + "\" not found");
    const auto& resource = res_it->second;
    if (!resource.is_array())
        throw ExecutionError("operator \"" + op.name + "\": recall_resource: resource \"" + resource_name + "\" is not an array, want []map[string]any");
    for (std::size_t i = 0; i < resource.as_array().size(); ++i) {
        const auto& elem = resource.as_array()[i];
        if (!elem.is_object())
            throw ExecutionError("operator \"" + op.name + "\": recall_resource: items[" + std::to_string(i) + "] is not an object, want map[string]any");
        Row row;
        for (const auto& [key, value] : elem.as_object()) row[key] = value;
        row["_source"] = JsonValue(op.name);
        frame.items.push_back(std::move(row));
    }
}

void run_transform_resource_lookup(Frame& frame, const OperatorConfig& op) {
    if (!frame.resources)
        throw ExecutionError("operator \"" + op.name + "\": transform_resource_lookup: no resource provider in context");
    const auto& params = op.params.as_object();
    auto rn_it = params.find("resource_name");
    if (rn_it == params.end() || !rn_it->second.is_string())
        throw ExecutionError("operator \"" + op.name + "\": transform_resource_lookup: missing resource_name");
    const std::string& resource_name = rn_it->second.as_string();
    auto res_it = frame.resources->find(resource_name);
    if (res_it == frame.resources->end())
        throw ExecutionError("operator \"" + op.name + "\": transform_resource_lookup: resource \"" + resource_name + "\" not found");
    const auto& resource = res_it->second;
    if (!resource.is_object())
        throw ExecutionError("operator \"" + op.name + "\": transform_resource_lookup: resource \"" + resource_name + "\" is not an object, want map[string]any");
    const auto& table = resource.as_object();

    auto lk_it = params.find("lookup_key");
    if (lk_it == params.end() || !lk_it->second.is_string())
        throw ExecutionError("operator \"" + op.name + "\": transform_resource_lookup: missing lookup_key");
    const std::string& lookup_key = lk_it->second.as_string();

    auto of_it = params.find("output_field");
    if (of_it == params.end() || !of_it->second.is_string())
        throw ExecutionError("operator \"" + op.name + "\": transform_resource_lookup: missing output_field");
    const std::string& output_field = of_it->second.as_string();

    auto dv_it = params.find("default_value");
    bool has_default = (dv_it != params.end());

    for (std::size_t i = 0; i < frame.items.size(); ++i) {
        auto it = frame.items[i].find(lookup_key);
        if (it == frame.items[i].end() || it->second.is_null()) {
            if (has_default) frame.items[i][output_field] = dv_it->second;
            continue;
        }
        std::string key;
        if (it->second.is_string()) {
            key = it->second.as_string();
        } else if (it->second.is_number()) {
            double d = it->second.as_number();
            if (d == static_cast<double>(static_cast<int64_t>(d))) {
                key = std::to_string(static_cast<int64_t>(d));
            } else {
                char buf[32];
                auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d);
                key = std::string(buf, ptr);
            }
        } else {
            key = sprint_value(it->second);
        }
        auto val_it = table.find(key);
        if (val_it != table.end()) {
            frame.items[i][output_field] = val_it->second;
        } else if (has_default) {
            frame.items[i][output_field] = dv_it->second;
        }
    }
}

std::string build_key_suffix(const Frame& frame, const std::vector<std::string>& fields) {
    if (fields.empty()) return "";
    std::string result;
    for (std::size_t i = 0; i < fields.size(); ++i) {
        if (i > 0) result += ':';
        auto it = frame.common.find(fields[i]);
        if (it != frame.common.end()) result += sprint_value(it->second);
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

void run_transform_redis_set(Frame& frame, const OperatorConfig& op) {
    auto rp = parse_redis_params(op);
    if (rp.host.empty()) return;

    int n = static_cast<int>(op.metadata.common_input.size());
    if (n < 2)
        throw ExecutionError("operator \"" + op.name + "\": transform_redis_set: common_input must have at least 2 fields (key fields + value field)");

    std::vector<std::string> key_fields(op.metadata.common_input.begin(), op.metadata.common_input.begin() + (n - 1));
    std::string key = rp.key_prefix + build_key_suffix(frame, key_fields);

    auto val_it = frame.common.find(op.metadata.common_input.back());
    JsonValue value = (val_it != frame.common.end()) ? val_it->second : JsonValue();

    std::unique_ptr<redis::Client> client;
    try {
        client = std::make_unique<redis::Client>(rp.host, rp.port, rp.password, rp.db);
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_set: write key " + key + ": " + e.what());
        return;
    }
    if (!client->connected()) {
        if (rp.fail_on_error)
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_set: write key " + key + ": connection failed");
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
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_set: unsupported data_type \"" + rp.data_type + "\"");
        }
    } catch (const ExecutionError&) {
        throw;
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_set: write key " + key + ": " + e.what());
    }
}

void run_transform_redis_get(Frame& frame, const OperatorConfig& op) {
    auto rp = parse_redis_params(op);
    const auto& result_field = op.metadata.common_output.at(0);
    const auto& cache_hit_field = op.metadata.common_output.at(1);

    if (rp.host.empty()) {
        frame.common[cache_hit_field] = JsonValue(false);
        return;
    }

    std::string key = rp.key_prefix + build_key_suffix(frame, op.metadata.common_input);

    std::unique_ptr<redis::Client> client;
    try {
        client = std::make_unique<redis::Client>(rp.host, rp.port, rp.password, rp.db);
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_get: " + std::string(e.what()));
        frame.common[cache_hit_field] = JsonValue(false);
        return;
    }
    if (!client->connected()) {
        if (rp.fail_on_error)
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_get: connection failed");
        frame.common[cache_hit_field] = JsonValue(false);
        return;
    }

    try {
        if (rp.data_type == "string") {
            auto val = client->get(key);
            if (val && !val->empty()) {
                frame.common[result_field] = JsonValue(*val);
                frame.common[cache_hit_field] = JsonValue(true);
            } else {
                frame.common[cache_hit_field] = JsonValue(false);
            }
        } else if (rp.data_type == "set") {
            auto members = client->smembers(key);
            if (!members.empty()) {
                JsonValue::array_t arr;
                for (auto& m : members) arr.push_back(JsonValue(std::move(m)));
                frame.common[result_field] = JsonValue(std::move(arr));
                frame.common[cache_hit_field] = JsonValue(true);
            } else {
                frame.common[cache_hit_field] = JsonValue(false);
            }
        } else if (rp.data_type == "list") {
            auto vals = client->lrange(key, 0, -1);
            if (!vals.empty()) {
                JsonValue::array_t arr;
                for (auto& v : vals) arr.push_back(JsonValue(std::move(v)));
                frame.common[result_field] = JsonValue(std::move(arr));
                frame.common[cache_hit_field] = JsonValue(true);
            } else {
                frame.common[cache_hit_field] = JsonValue(false);
            }
        } else {
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_get: unsupported data_type \"" + rp.data_type + "\"");
        }
    } catch (const ExecutionError&) {
        throw;
    } catch (const std::exception& e) {
        if (rp.fail_on_error)
            throw ExecutionError("operator \"" + op.name + "\": transform_redis_get: " + std::string(e.what()));
        frame.common[cache_hit_field] = JsonValue(false);
    }
}

void run_reorder_sort(Frame& frame, const OperatorConfig& op) {
    if (frame.items.empty()) return;
    const std::string order = [&]() {
        const auto& obj = op.params.as_object();
        auto it = obj.find("order");
        return it == obj.end() ? std::string("desc") : it->second.as_string();
    }();
    if (op.metadata.item_input.empty()) throw ExecutionError("operator \"" + op.name + "\": reorder_sort requires item_input field");
    const std::string field = op.metadata.item_input.front();
    std::vector<std::pair<double, Row>> keyed;
    keyed.reserve(frame.items.size());
    for (std::size_t i = 0; i < frame.items.size(); ++i) {
        try {
            keyed.push_back({to_double(require_item(frame, op, i, field)), frame.items[i]});
        } catch (const OperatorError& err) {
            throw ExecutionError("operator \"" + op.name + "\": reorder_sort: item[" + std::to_string(i) + "]." + field + ": " + err.what());
        }
    }
    if (order == "asc") {
        std::stable_sort(keyed.begin(), keyed.end(), [](const auto& a, const auto& b) { return a.first < b.first; });
    } else if (order == "desc") {
        std::stable_sort(keyed.begin(), keyed.end(), [](const auto& a, const auto& b) { return a.first > b.first; });
    } else {
        throw ExecutionError("operator \"" + op.name + "\": reorder_sort: unsupported order \"" + order + "\"");
    }
    for (std::size_t i = 0; i < keyed.size(); ++i) frame.items[i] = std::move(keyed[i].second);
}

void run_transform_by_lua(Frame& frame, const OperatorConfig& op) {
    const auto& params = op.params.as_object();
    auto script_it = params.find("lua_script");
    if (script_it == params.end() || !script_it->second.is_string())
        throw ExecutionError("lua: exactly one of function_for_item or function_for_common must be set");

    auto fi_it = params.find("function_for_item");
    auto fc_it = params.find("function_for_common");
    std::string func_for_item = (fi_it != params.end() && fi_it->second.is_string()) ? fi_it->second.as_string() : "";
    std::string func_for_common = (fc_it != params.end() && fc_it->second.is_string()) ? fc_it->second.as_string() : "";

    if (func_for_item.empty() && func_for_common.empty())
        throw ExecutionError("lua: exactly one of function_for_item or function_for_common must be set");
    if (!func_for_item.empty() && !func_for_common.empty())
        throw ExecutionError("lua: cannot set both function_for_item and function_for_common");

    auto resolve_common = [&](const std::string& field) -> JsonValue {
        auto it = frame.common.find(field);
        if (it != frame.common.end() && !it->second.is_null()) return it->second;
        auto def = op.common_defaults.find(field);
        if (def != op.common_defaults.end()) return def->second;
        return (it != frame.common.end()) ? it->second : JsonValue();
    };
    auto resolve_item = [&](std::size_t idx, const std::string& field) -> JsonValue {
        auto it = frame.items[idx].find(field);
        if (it != frame.items[idx].end() && !it->second.is_null()) return it->second;
        auto def = op.item_defaults.find(field);
        if (def != op.item_defaults.end()) return def->second;
        return (it != frame.items[idx].end()) ? it->second : JsonValue();
    };

    lua::LuaVM vm;
    vm.load_script(script_it->second.as_string(), op.name);

    if (!func_for_item.empty()) {
        int nret = static_cast<int>(op.metadata.item_output.size());
        for (const auto& field : op.metadata.common_input)
            vm.set_global(field, resolve_common(field));
        for (std::size_t i = 0; i < frame.items.size(); ++i) {
            for (const auto& field : op.metadata.item_input)
                vm.set_global(field, resolve_item(i, field));
            auto results = vm.call_function(func_for_item, nret, op.name);
            for (int j = 0; j < nret; ++j)
                frame.items[i][op.metadata.item_output[static_cast<std::size_t>(j)]] = results[static_cast<std::size_t>(j)];
        }
    } else {
        int nret = static_cast<int>(op.metadata.common_output.size());
        for (const auto& field : op.metadata.common_input)
            vm.set_global(field, resolve_common(field));
        for (const auto& field : op.metadata.item_input) {
            std::vector<JsonValue> column;
            column.reserve(frame.items.size());
            for (std::size_t i = 0; i < frame.items.size(); ++i)
                column.push_back(resolve_item(i, field));
            vm.set_global_table(field, column);
        }
        auto results = vm.call_function(func_for_common, nret, op.name);
        for (int j = 0; j < nret; ++j)
            frame.common[op.metadata.common_output[static_cast<std::size_t>(j)]] = results[static_cast<std::size_t>(j)];
    }
}

Result project_result(const Frame& frame, const FlowContract& contract) {
    Result result;
    for (const auto& field : contract.common_output) {
        auto it = frame.common.find(field);
        if (it != frame.common.end()) result.common[field] = it->second;
    }
    for (const auto& item : frame.items) {
        Row row;
        for (const auto& field : contract.item_output) {
            auto it = item.find(field);
            if (it != item.end()) row[field] = it->second;
        }
        result.items.push_back(std::move(row));
    }
    return result;
}

void validate_request(const Request& request, const FlowContract& contract) {
    for (const auto& field : contract.common_input) if (!request.common.count(field)) throw ValidationError("missing required common input field \"" + field + "\"");
    for (std::size_t i = 0; i < request.items.size(); ++i) {
        for (const auto& field : contract.item_input) if (!request.items[i].count(field)) throw ValidationError("item[" + std::to_string(i) + "] missing required item input field \"" + field + "\"");
    }
}

}  // namespace

Engine::Engine(Config config) : config_(std::move(config)) {
    expanded_ = expand_operator_sequence_with_subflows(config_);
    graph_ = build_dag(config_, expanded_);
}

Engine Engine::from_file(const std::string& path) { return Engine(load_config_from_file(path)); }

Result Engine::execute(const Request& request) const {
    static const std::map<std::string, JsonValue> empty_resources;
    return execute(request, empty_resources);
}

Result Engine::execute(const Request& request, const std::map<std::string, JsonValue>& resources) const {
    validate_request(request, config_.flow_contract);
    Frame frame;
    frame.common = request.common;
    frame.items = request.items;
    frame.resources = &resources;
    for (const auto& name : expanded_.sequence) {
        const auto& op = config_.operators.at(name);
        if (should_skip(frame, op)) continue;
        for (std::size_t i = 0; i < frame.items.size(); ++i) {
            for (const auto& field : op.metadata.item_input) {
                if (op.item_defaults.count(field)) continue;
                auto it = frame.items[i].find(field);
                if (it == frame.items[i].end() || it->second.is_null()) {
                    throw ExecutionError("operator \"" + op.name + "\": required field \"" + field + "\" is nil on item[" + std::to_string(i) + "]");
                }
            }
        }
        if (op.type_name == "transform_copy") run_transform_copy(frame, op);
        else if (op.type_name == "transform_dispatch") run_transform_dispatch(frame, op);
        else if (op.type_name == "transform_size") run_transform_size(frame, op);
        else if (op.type_name == "transform_normalize") run_transform_normalize(frame, op);
        else if (op.type_name == "transform_by_lua") run_transform_by_lua(frame, op);
        else if (op.type_name == "filter_truncate") run_filter_truncate(frame, op);
        else if (op.type_name == "filter_condition") run_filter_condition(frame, op);
        else if (op.type_name == "filter_paginate") run_filter_paginate(frame, op);
        else if (op.type_name == "recall_static") run_recall_static(frame, op);
        else if (op.type_name == "recall_resource") run_recall_resource(frame, op);
        else if (op.type_name == "transform_resource_lookup") run_transform_resource_lookup(frame, op);
        else if (op.type_name == "transform_redis_set") run_transform_redis_set(frame, op);
        else if (op.type_name == "transform_redis_get") run_transform_redis_get(frame, op);
        else if (op.type_name == "reorder_sort") run_reorder_sort(frame, op);
        else if (op.type_name == "reorder_shuffle_by_salt") run_reorder_shuffle_by_salt(frame, op);
        else if (op.type_name == "merge_dedup") run_merge_dedup(frame, op);
        else if (op.type_name == "observe_log") run_observe_log(frame, op);
        else throw RegistryError("operator \"" + op.name + "\": operator type not registered: \"" + op.type_name + "\"");
    }
    return project_result(frame, config_.flow_contract);
}

std::string Engine::render_dag(const std::string& format, int collapse) const {
    if (format == "dot") return collapse > 0 ? render_collapsed_dot(graph_, collapse) : render_dot(graph_);
    if (format == "mermaid") return collapse > 0 ? render_collapsed_mermaid(graph_, collapse) : render_mermaid(graph_);
    throw ValidationError("unsupported DAG format \"" + format + "\" (use \"dot\" or \"mermaid\")");
}

}  // namespace pine
