#include "pine/pine.hpp"
#include "lua/lua_bridge.hpp"

#include <algorithm>
#include <cmath>
#include <map>
#include <set>
#include <sstream>

namespace pine {
namespace {

using Row = std::map<std::string, JsonValue>;

struct Frame {
    std::map<std::string, JsonValue> common;
    std::vector<Row> items;
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
        return;
    }
    throw ExecutionError("operator \"" + op.name + "\": transform_copy direction not implemented in pine-cpp MVP");
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
    validate_request(request, config_.flow_contract);
    Frame frame{request.common, request.items};
    for (const auto& name : expanded_.sequence) {
        const auto& op = config_.operators.at(name);
        if (should_skip(frame, op)) continue;
        if (op.type_name == "transform_copy") run_transform_copy(frame, op);
        else if (op.type_name == "filter_truncate") run_filter_truncate(frame, op);
        else if (op.type_name == "recall_static") run_recall_static(frame, op);
        else if (op.type_name == "reorder_sort") run_reorder_sort(frame, op);
        else if (op.type_name == "transform_by_lua") run_transform_by_lua(frame, op);
        else throw ExecutionError("operator \"" + op.name + "\": unsupported operator type \"" + op.type_name + "\" in pine-cpp MVP");
    }
    return project_result(frame, config_.flow_contract);
}

std::string Engine::render_dag(const std::string& format, int collapse) const {
    if (format == "dot") return collapse > 0 ? render_collapsed_dot(graph_, collapse) : render_dot(graph_);
    if (format == "mermaid") return collapse > 0 ? render_collapsed_mermaid(graph_, collapse) : render_mermaid(graph_);
    throw ValidationError("unsupported DAG format \"" + format + "\" (use \"dot\" or \"mermaid\")");
}

}  // namespace pine
