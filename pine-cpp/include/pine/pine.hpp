#pragma once

#include <map>
#include <optional>
#include <set>
#include <stdexcept>
#include <string>
#include <utility>
#include <variant>
#include <vector>

namespace pine {

class Error : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

class ConfigError : public Error {
public:
    using Error::Error;
};

class ValidationError : public Error {
public:
    using Error::Error;
};

class RegistryError : public Error {
public:
    using Error::Error;
};

class ExecutionError : public Error {
public:
    using Error::Error;
};

class JsonValue {
public:
    using object_t = std::map<std::string, JsonValue>;
    using array_t = std::vector<JsonValue>;
    using value_t = std::variant<std::nullptr_t, bool, double, std::string, array_t, object_t>;

    JsonValue();
    JsonValue(std::nullptr_t);
    JsonValue(bool value);
    JsonValue(double value);
    JsonValue(int value);
    JsonValue(std::string value);
    JsonValue(const char* value);
    JsonValue(array_t value);
    JsonValue(object_t value);

    bool is_null() const;
    bool is_bool() const;
    bool is_number() const;
    bool is_string() const;
    bool is_array() const;
    bool is_object() const;

    bool as_bool() const;
    double as_number() const;
    const std::string& as_string() const;
    const array_t& as_array() const;
    const object_t& as_object() const;
    array_t& as_array();
    object_t& as_object();

    bool truthy() const;

    const JsonValue* find(const std::string& key) const;
    JsonValue* find(const std::string& key);

private:
    value_t value_;
};

JsonValue parse_json(const std::string& text);
std::string dump_json(const JsonValue& value, int indent = 2);

struct Metadata {
    std::vector<std::string> common_input;
    std::vector<std::string> common_output;
    std::vector<std::string> item_input;
    std::vector<std::string> item_output;
};

struct OperatorConfig {
    std::string name;
    std::string type_name;
    Metadata metadata;
    std::vector<std::string> skip;
    bool recall = false;
    bool consumes_row_set = false;
    bool mutates_row_set = false;
    bool additive_writes_row_set = false;
    std::map<std::string, JsonValue> common_defaults;
    std::map<std::string, JsonValue> item_defaults;
    std::vector<std::string> sources;
    JsonValue params;
    std::string operator_type;
    int data_parallel = 0;
};

struct FlowContract {
    std::vector<std::string> common_input;
    std::vector<std::string> item_input;
    std::vector<std::string> common_output;
    std::vector<std::string> item_output;
};

struct Config {
    std::map<std::string, OperatorConfig> operators;
    std::map<std::string, std::vector<std::string>> pipeline_map;
    std::map<std::string, std::vector<std::string>> pipeline_group;
    FlowContract flow_contract;
    std::string storage_mode = "row";
};

struct ExpandedSequence {
    std::vector<std::string> sequence;
    std::map<std::string, std::string> op_to_subflow;
};

Config load_config_from_file(const std::string& path);
Config load_config_from_json(const std::string& text);
ExpandedSequence expand_operator_sequence_with_subflows(const Config& config);

// --- Operator Registry (equivalent to Go's pine.Register + marker interfaces) ---

struct ParamSchema {
    std::string type;
    bool required = false;
    JsonValue default_value;            // null means no default
    std::string description;
};

struct OperatorTraits {
    std::string operator_type;          // "recall", "transform", "filter", etc.
    bool consumes_row_set = false;
    bool mutates_row_set = false;
    bool additive_writes_row_set = false;
    bool concurrent_safe = false;
    std::string schema_type;            // Capitalized: "Filter", "Transform", etc.
    std::string description;
    std::map<std::string, ParamSchema> params;
};

const OperatorTraits* registry_lookup(const std::string& type_name);
void apply_registry_traits(Config& config);
std::string export_schema_json();

struct Node {
    std::string name;
    std::string subflow;
    std::vector<int> preds;
    std::vector<int> succs;
    const OperatorConfig* config = nullptr;
};

struct Graph {
    std::vector<Node> nodes;
    std::map<std::string, int> name_to_index;
};

Graph build_dag(const Config& config, const ExpandedSequence& expanded);
std::string render_dot(const Graph& graph);
std::string render_mermaid(const Graph& graph);
std::string render_collapsed_dot(const Graph& graph, int level);
std::string render_collapsed_mermaid(const Graph& graph, int level);

struct Request {
    std::map<std::string, JsonValue> common;
    std::vector<std::map<std::string, JsonValue>> items;
};

struct Result {
    std::map<std::string, JsonValue> common;
    std::vector<std::map<std::string, JsonValue>> items;
};

struct OpTrace {
    std::string name;
    int64_t duration_us = 0;
    bool skipped = false;
};

struct TracedResult {
    Result result;
    std::vector<OpTrace> trace;
    std::vector<std::string> warnings;
};

class Engine {
public:
    explicit Engine(Config config);
    static Engine from_file(const std::string& path);

    Result execute(const Request& request) const;
    Result execute(const Request& request, const std::map<std::string, JsonValue>& resources) const;
    TracedResult execute_traced(const Request& request, const std::map<std::string, JsonValue>& resources) const;
    std::string render_dag(const std::string& format, int collapse = 0) const;

    const ExpandedSequence& expanded() const { return expanded_; }

private:
    Config config_;
    ExpandedSequence expanded_;
    Graph graph_;
};

Request load_request_from_file(const std::string& path);
std::string result_to_json(const Result& result);

}  // namespace pine
