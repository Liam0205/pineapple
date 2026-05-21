#include "pine/pine.hpp"

#include <algorithm>
#include <map>
#include <vector>

namespace pine {
namespace {

// Each operator declares its own traits, equivalent to Go's marker interfaces
// (ConsumesRowSetMarker, MutatesRowSetMarker, AdditiveWritesRowSetMarker, ConcurrentSafeMarker).
// Source of truth: pine-go/operators/*/

const std::map<std::string, OperatorTraits>& builtin_registry() {
    static const std::map<std::string, OperatorTraits> registry = {
        // --- filter (ConsumesRowSet + MutatesRowSet) ---
        {"filter_condition", {
            "filter", true, true, false, false,
            "Filter",
            "Removes items where a specified field equals a given value.",
            {
                {"value", {"any", true, JsonValue(nullptr), "Items where field == value are removed."}},
            }
        }},
        {"filter_paginate", {
            "filter", true, true, false, false,
            "Filter",
            "Keeps only items in the [page*size, page*size+size) range, removes the rest.",
            {}
        }},
        {"filter_truncate", {
            "filter", true, true, false, false,
            "Filter",
            "Keeps only the first N items, removing the rest.",
            {
                {"top_n", {"int64", true, JsonValue(nullptr), "Number of items to keep."}},
            }
        }},

        // --- merge (ConsumesRowSet + MutatesRowSet) ---
        {"merge_dedup", {
            "merge", true, true, false, false,
            "Merge",
            "Deduplicates items by a key field, keeping the first occurrence.",
            {
                {"strategy", {"string", false, JsonValue("first"), "Dedup strategy \xe2\x80\x94 \"first\" keeps first occurrence."}},
            }
        }},

        // --- observe ---
        {"observe_log", {
            "observe", false, false, false, false,
            "Observe",
            "Reads declared input fields and writes them to Go standard log. This is a read-only operator: it produces no output fields and does not modify the DataFrame. It is exempt from dead-code detection.",
            {
                {"log_prefix", {"string", false, JsonValue(""), "Prefix prepended to each log line."}},
            }
        }},

        // --- recall (AdditiveWritesRowSet) ---
        {"recall_resource", {
            "recall", false, false, true, false,
            "Recall",
            "Recalls items from a named resource.",
            {
                {"resource_name", {"string", true, JsonValue(nullptr), "Name of the resource to read."}},
            }
        }},
        {"recall_static", {
            "recall", false, false, true, false,
            "Recall",
            "Emits a configurable static set of items for testing and validation.",
            {
                {"items", {"any", true, JsonValue(nullptr), "JSON array of item maps to emit as candidates."}},
            }
        }},

        // --- reorder (ConsumesRowSet + MutatesRowSet) ---
        {"reorder_shuffle_by_salt", {
            "reorder", true, true, false, false,
            "Reorder",
            "Deterministic hash-based shuffle using a caller-provided salt.",
            {}
        }},
        {"reorder_sort", {
            "reorder", true, true, false, false,
            "Reorder",
            "Sorts items by a numeric field in ascending or descending order.",
            {
                {"order", {"string", false, JsonValue("desc"), "Sort direction \xe2\x80\x94 \"asc\" or \"desc\"."}},
            }
        }},

        // --- transform ---
        {"transform_by_lua", {
            "transform", false, false, false, true,
            "Transform",
            "Executes a Lua script for per-item or per-common computation.",
            {
                {"function_for_common", {"string", false, JsonValue(""), "Function name to call once for all items."}},
                {"function_for_item", {"string", false, JsonValue(""), "Function name to call per item."}},
                {"lua_script", {"string", true, JsonValue(nullptr), "Lua source code defining the function to call."}},
            }
        }},
        {"transform_by_remote_pineapple", {
            "transform", true, false, false, true,
            "Transform",
            "Calls a downstream Pineapple service and maps response fields back to the local frame.",
            {
                {"allow_private", {"bool", false, JsonValue(false), "Allow connections to private/loopback addresses (dev/internal use)."}},
                {"common_request", {"any", false, JsonValue(nullptr), "Downstream common field names, positionally mapped to common_input."}},
                {"common_response", {"any", false, JsonValue(nullptr), "Downstream common response field names, positionally mapped to common_output."}},
                {"endpoint", {"string", false, JsonValue("/execute"), "Downstream endpoint path."}},
                {"fail_on_error", {"bool", false, JsonValue(true), "true=fatal on downstream error; false=warning and skip."}},
                {"host", {"string", true, JsonValue(nullptr), "Downstream service host."}},
                {"item_request", {"any", false, JsonValue(nullptr), "Downstream item field names, positionally mapped to item_input."}},
                {"item_response", {"any", false, JsonValue(nullptr), "Downstream item response field names, positionally mapped to item_output."}},
                {"max_response_size", {"int64", false, JsonValue(10485760.0), "Maximum response body size in bytes (default 10 MB)."}},
                {"port", {"int64", true, JsonValue(nullptr), "Downstream service port."}},
                {"timeout", {"float64", false, JsonValue(5.0), "Request timeout in seconds."}},
            }
        }},
        {"transform_copy", {
            "transform", false, false, false, true,
            "Transform",
            "Copies field values between common and item dimensions.",
            {
                {"direction", {"string", true, JsonValue(nullptr), "Copy direction: \"common_to_item\", \"item_to_common\", \"common_to_common\", or \"item_to_item\"."}},
            }
        }},
        {"transform_dispatch", {
            "transform", false, false, false, true,
            "Transform",
            "Copies a common-side field value to every item as an item-side field.",
            {}
        }},
        {"transform_normalize", {
            "transform", false, false, false, false,
            "Transform",
            "Normalizes a numeric item field using min-max scaling to [0, 1].",
            {
                {"method", {"string", false, JsonValue("min_max"), "Normalization method."}},
            }
        }},
        {"transform_redis_get", {
            "transform", false, false, false, true,
            "Transform",
            "Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.",
            {
                {"data_type", {"string", false, JsonValue("string"), "Redis data type: \"set\", \"string\", or \"list\"."}},
                {"fail_on_error", {"bool", false, JsonValue(false), "Return fatal error on Redis infrastructure failure instead of treating as cache miss."}},
                {"key_prefix", {"string", true, JsonValue(nullptr), "Key prefix prepended to the suffix built from common_input fields."}},
                {"redis_addr", {"string", true, JsonValue(nullptr), "Redis server address (host:port)."}},
                {"redis_db", {"int", false, JsonValue(0.0), "Redis DB number."}},
                {"redis_password", {"string", false, JsonValue(""), "Redis password."}},
            }
        }},
        {"transform_redis_set", {
            "transform", false, false, false, true,
            "Transform",
            "Generic Redis write operator. Writes a value by key with optional TTL.",
            {
                {"data_type", {"string", false, JsonValue("string"), "Redis data type: \"set\", \"string\", or \"list\"."}},
                {"fail_on_error", {"bool", false, JsonValue(false), "Return fatal error on Redis infrastructure failure instead of logging and continuing."}},
                {"key_prefix", {"string", true, JsonValue(nullptr), "Key prefix prepended to the suffix built from common_input fields."}},
                {"redis_addr", {"string", true, JsonValue(nullptr), "Redis server address (host:port)."}},
                {"redis_db", {"int", false, JsonValue(0.0), "Redis DB number."}},
                {"redis_password", {"string", false, JsonValue(""), "Redis password."}},
                {"ttl", {"int", false, JsonValue(0.0), "TTL in seconds. 0 means no expiry."}},
            }
        }},
        {"transform_resource_lookup", {
            "transform", false, false, false, true,
            "Transform",
            "Enriches items by looking up values from a named resource.",
            {
                {"default_value", {"any", false, JsonValue(nullptr), "Value to use when the key is not found. Missing keys are skipped if unset."}},
                {"lookup_key", {"string", true, JsonValue(nullptr), "Item field whose value is used as the lookup key."}},
                {"output_field", {"string", true, JsonValue(nullptr), "Item field to write the looked-up value to."}},
                {"resource_name", {"string", true, JsonValue(nullptr), "Name of the resource to read."}},
            }
        }},
        {"transform_size", {
            "transform", true, false, false, true,
            "Transform",
            "Outputs the current item count to a common field.",
            {}
        }},
    };
    return registry;
}

}  // namespace

const OperatorTraits* registry_lookup(const std::string& type_name) {
    const auto& reg = builtin_registry();
    auto it = reg.find(type_name);
    return it != reg.end() ? &it->second : nullptr;
}

void apply_registry_traits(Config& config) {
    for (auto& [name, op] : config.operators) {
        const auto* traits = registry_lookup(op.type_name);
        if (!traits) {
            throw RegistryError("operator \"" + name + "\": operator type not registered: \"" + op.type_name + "\"");
        }
        op.operator_type = traits->operator_type;
        if (traits->consumes_row_set) op.consumes_row_set = true;
        if (traits->mutates_row_set) op.mutates_row_set = true;
        if (traits->additive_writes_row_set) op.additive_writes_row_set = true;
    }
}

std::string export_schema_json() {
    const auto& reg = builtin_registry();

    // Collect and sort by name (map is already sorted alphabetically)
    std::vector<std::pair<std::string, const OperatorTraits*>> entries;
    for (const auto& [name, traits] : reg) {
        entries.emplace_back(name, &traits);
    }
    std::sort(entries.begin(), entries.end(),
              [](const auto& a, const auto& b) { return a.first < b.first; });

    // Build JSON array
    JsonValue::array_t arr;
    for (const auto& [name, traits] : entries) {
        JsonValue::object_t obj;
        obj["Name"] = JsonValue(name);
        obj["Type"] = JsonValue(traits->schema_type);
        obj["Description"] = JsonValue(traits->description);

        JsonValue::object_t params_obj;
        for (const auto& [pname, pschema] : traits->params) {
            JsonValue::object_t param;
            param["Type"] = JsonValue(pschema.type);
            param["Required"] = JsonValue(pschema.required);
            param["Default"] = pschema.default_value;
            param["Description"] = JsonValue(pschema.description);
            params_obj[pname] = JsonValue(param);
        }
        obj["Params"] = JsonValue(params_obj);

        arr.push_back(JsonValue(obj));
    }

    return dump_json(JsonValue(arr));
}

}  // namespace pine
