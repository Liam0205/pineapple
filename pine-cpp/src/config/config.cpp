#include "pine/pine.hpp"
#include "pine/template.hpp"

#include <algorithm>
#include <fstream>
#include <sstream>

namespace pine {
namespace {

std::string read_file(const std::string& path) {
  std::ifstream input(path);
  if (!input) {
    throw ConfigError("error reading config: " + path);
  }
  std::ostringstream oss;
  oss << input.rdbuf();
  return oss.str();
}

std::vector<std::string> as_string_list(const Variant::object_t& obj, const std::string& key) {
  std::vector<std::string> out;
  auto it = obj.find(key);
  if (it == obj.end()) {
    return out;
  }
  for (const auto& item : it->second.as_array()) {
    out.push_back(item.as_string());
  }
  return out;
}

std::map<std::string, Variant> as_value_map(const Variant::object_t& obj, const std::string& key) {
  std::map<std::string, Variant> out;
  auto it = obj.find(key);
  if (it == obj.end()) {
    return out;
  }
  for (const auto& [name, value] : it->second.as_object()) {
    out[name] = value;
  }
  return out;
}

Metadata parse_metadata(const Variant::object_t& obj) {
  Metadata meta;
  auto it = obj.find("$metadata");
  if (it == obj.end()) {
    throw ConfigError("operator missing $metadata");
  }
  const auto& mo = it->second.as_object();
  meta.common_input = as_string_list(mo, "common_input");
  meta.common_input_skip = as_string_list(mo, "common_input_skip");
  meta.common_input_template = as_string_list(mo, "common_input_template");
  meta.common_output = as_string_list(mo, "common_output");
  meta.item_input = as_string_list(mo, "item_input");
  meta.item_output = as_string_list(mo, "item_output");
  return meta;
}

std::vector<std::string> parse_skip(const Variant::object_t& obj) {
  std::vector<std::string> out;
  auto it = obj.find("skip");
  if (it == obj.end()) {
    return out;
  }
  if (it->second.is_string()) {
    if (!it->second.as_string().empty()) {
      out.push_back(it->second.as_string());
    }
    return out;
  }
  for (const auto& item : it->second.as_array()) {
    out.push_back(item.as_string());
  }
  return out;
}

OperatorConfig parse_operator(const std::string& name, const Variant& value) {
  const auto& obj = value.as_object();
  OperatorConfig op;
  op.name = name;
  auto type_it = obj.find("type_name");
  if (type_it == obj.end()) {
    throw ConfigError("operator \"" + name + "\": missing type_name");
  }
  op.type_name = type_it->second.as_string();
  op.metadata = parse_metadata(obj);
  op.skip = parse_skip(obj);
  if (auto it = obj.find("recall"); it != obj.end() && it->second.is_bool()) {
    op.recall = it->second.as_bool();
  }
  if (auto it = obj.find("consumes_row_set"); it != obj.end() && it->second.is_bool()) {
    op.consumes_row_set = it->second.as_bool();
  }
  if (auto it = obj.find("mutates_row_set"); it != obj.end() && it->second.is_bool()) {
    op.mutates_row_set = it->second.as_bool();
  }
  if (auto it = obj.find("additive_writes_row_set"); it != obj.end() && it->second.is_bool()) {
    op.additive_writes_row_set = it->second.as_bool();
  }
  if (auto it = obj.find("debug"); it != obj.end() && it->second.is_bool()) {
    op.debug = it->second.as_bool();
  }
  if (auto it = obj.find("for_branch_control"); it != obj.end() && it->second.is_bool()) {
    op.for_branch_control = it->second.as_bool();
  }
  if (auto it = obj.find("data_parallel"); it != obj.end() && it->second.is_number()) {
    op.data_parallel = static_cast<int>(it->second.as_number());
  }
  op.common_defaults = as_value_map(obj, "common_defaults");
  op.item_defaults = as_value_map(obj, "item_defaults");
  op.strict_common = as_string_list(obj, "strict_common");
  op.strict_item = as_string_list(obj, "strict_item");
  op.sources = as_string_list(obj, "sources");
  Variant::object_t params;
  for (const auto& [key, item] : obj) {
    if (key == "type_name" || key == "$metadata" || key == "$code_info" || key == "skip" || key == "recall" ||
        key == "sources" || key == "debug" || key == "consumes_row_set" || key == "mutates_row_set" ||
        key == "additive_writes_row_set" || key == "common_defaults" || key == "item_defaults" ||
        key == "strict_common" || key == "strict_item" || key == "for_branch_control" ||
        key == "data_parallel") {
      continue;
    }
    params[key] = item;
  }
  op.params = Variant(params);
  return op;
}

void validate_config(const Config& config) {
  if (config.operators.empty()) {
    throw ConfigError("pipeline_config.operators is empty");
  }
  if (config.pipeline_group.empty()) {
    throw ConfigError("pipeline_group is empty");
  }
  for (const auto& [name, op] : config.operators) {
    for (const auto& skip : op.skip) {
      if (skip.empty() || skip[0] != '_') {
        throw ConfigError("operator \"" + name + "\": skip field \"" + skip +
                          "\" must start with '_' (control fields are engine-internal)");
      }
      // Skip fields may live in either common_input (legacy layout)
      // or common_input_skip (#74 buckets). Either is sufficient for
      // DAG ordering; the operator-visible input filter strips them
      // regardless.
      const auto& ci = op.metadata.common_input;
      const auto& cis = op.metadata.common_input_skip;
      if (std::find(ci.begin(), ci.end(), skip) == ci.end() &&
          std::find(cis.begin(), cis.end(), skip) == cis.end()) {
        throw ConfigError("operator \"" + name + "\": skip field \"" + skip +
                          "\" must also appear in $metadata.common_input or $metadata.common_input_skip to "
                          "ensure correct DAG ordering");
      }
    }
    for (const auto& src : op.sources) {
      if (!config.operators.count(src)) {
        throw ConfigError("operator \"" + name + "\": sources references undefined operator \"" + src + "\"");
      }
    }
    if (op.additive_writes_row_set && op.mutates_row_set) {
      throw RegistryError("operator \"" + name +
                          "\": additive_writes_row_set and mutates_row_set are mutually exclusive");
    }
    if (op.type_name == "filter_condition") {
      if (!op.params.is_object() || op.params.as_object().find("value") == op.params.as_object().end()) {
        throw RegistryError("operator \"" + name +
                            "\": required parameter \"value\" missing for operator \"" + name + "\"");
      }
    }
    if (op.type_name == "filter_truncate") {
      auto pit = op.params.as_object().find("top_n");
      if (pit != op.params.as_object().end()) {
        if (pit->second.is_string()) {
          // Only a bare {{field}} marker is accepted here; the engine
          // resolves it per-request at execute time. A non-marker string
          // is hand-edited garbage and must surface as a registry error
          // rather than silently truncating to zero.
          if (!is_bare_marker(pit->second.as_string())) {
            throw RegistryError("operator \"" + name + "\": top_n must be numeric");
          }
        } else if (!pit->second.is_number()) {
          throw RegistryError("operator \"" + name + "\": top_n must be numeric");
        } else {
          double val = pit->second.as_number();
          if (val < 0) {
            throw RegistryError("operator \"" + name +
                                "\": filter_truncate: top_n must be non-negative, got " +
                                std::to_string(static_cast<int>(val)));
          }
        }
      }
    }
    if (op.type_name == "reorder_sort") {
      auto oit = op.params.as_object().find("order");
      if (oit != op.params.as_object().end() && oit->second.is_string()) {
        const auto& order = oit->second.as_string();
        if (order != "asc" && order != "desc") {
          throw RegistryError("operator \"" + name + "\": unsupported order \"" + order + "\"");
        }
      }
    }
    if (op.data_parallel < 0) {
      throw ValidationError("operator \"" + name + "\": data_parallel must be >= 1, got " +
                            std::to_string(op.data_parallel));
    }
    if (op.data_parallel > 1) {
      if (op.operator_type != "transform") {
        throw ValidationError("operator \"" + name + "\": data_parallel=" + std::to_string(op.data_parallel) +
                              " is only supported for Transform operators, got " + op.operator_type);
      }
      if (!op.metadata.common_output.empty()) {
        throw ValidationError("operator \"" + name + "\": data_parallel=" + std::to_string(op.data_parallel) +
                              " requires empty $metadata.common_output for Transform operators");
      }
      if (!op.concurrent_safe) {
        throw ValidationError("operator \"" + name + "\": data_parallel=" + std::to_string(op.data_parallel) +
                              " requires the operator to implement ConcurrentSafe interface (type \"" +
                              op.type_name + "\" does not)");
      }
    }
  }
  for (const auto& [name, _] : config.operators) {
    if (config.pipeline_map.count(name)) {
      throw ConfigError("name \"" + name + "\" exists in both operators and pipeline_map");
    }
  }
}

void expand_entries(const Config& config, const std::vector<std::string>& entries, const std::string& parent,
                    std::vector<std::string>& sequence, std::map<std::string, std::string>& mapping,
                    std::set<std::string>& visiting, std::set<std::string>& seen) {
  for (const auto& entry : entries) {
    if (config.operators.count(entry)) {
      if (seen.count(entry)) {
        throw ConfigError("operator \"" + entry + "\" referenced more than once in pipeline tree");
      }
      seen.insert(entry);
      sequence.push_back(entry);
      mapping[entry] = parent;
    } else if (config.pipeline_map.count(entry)) {
      if (visiting.count(entry)) {
        throw ConfigError("cycle detected in sub-flow expansion: \"" + entry + "\"");
      }
      visiting.insert(entry);
      expand_entries(config, config.pipeline_map.at(entry), entry, sequence, mapping, visiting, seen);
      visiting.erase(entry);
    } else {
      throw ConfigError("pipeline entry \"" + entry + "\" is neither an operator nor a sub-flow");
    }
  }
}

}  // namespace

Config load_config_from_file(const std::string& path) {
  return load_config_from_json(read_file(path));
}

Config load_config_from_json(const std::string& text) {
  const auto root = parse_json(text).as_object();
  auto require_obj = [](const Variant::object_t& parent, const std::string& key) -> const Variant::object_t& {
    auto it = parent.find(key);
    if (it == parent.end()) {
      throw ConfigError("missing required top-level field \"" + key + "\"");
    }
    return it->second.as_object();
  };
  Config config;
  if (auto it = root.find("storage_mode"); it != root.end()) {
    config.storage_mode = it->second.as_string();
  }
  if (auto it = root.find("debug"); it != root.end() && it->second.is_bool()) {
    config.debug = it->second.as_bool();
  }
  if (auto it = root.find("log_prefix"); it != root.end() && it->second.is_string()) {
    config.log_prefix = it->second.as_string();
  }
  if (auto it = root.find("_PINEAPPLE_VERSION"); it != root.end() && it->second.is_string()) {
    config.pineapple_version = it->second.as_string();
  }
  if (auto it = root.find("_PINEAPPLE_CREATE_TIME"); it != root.end() && it->second.is_string()) {
    config.pineapple_create_time = it->second.as_string();
  }
  if (auto it = root.find("resource_config"); it != root.end() && it->second.is_object()) {
    for (const auto& [name, entry] : it->second.as_object()) {
      if (!entry.is_object()) {
        throw ConfigError("resource_config entry \"" + name + "\" must be an object");
      }
      const auto& eo = entry.as_object();
      ResourceEntry re;
      if (auto tit = eo.find("type"); tit != eo.end() && tit->second.is_string()) {
        re.type = tit->second.as_string();
      }
      if (auto iit = eo.find("interval"); iit != eo.end() && iit->second.is_number()) {
        re.interval = static_cast<int>(iit->second.as_number());
      }
      if (auto pit = eo.find("params"); pit != eo.end()) {
        re.params = pit->second;
      }
      config.resource_config[name] = std::move(re);
    }
  }
  // Follow-up: flow_contract is optional in pine-go (struct field
  // with omitempty semantics defaults to empty FlowContract{}). Match
  // that — only parse the four input/output lists when the field is
  // present. Caught by differential fuzz: 17/50 smoke
  // divergences all hit this strictness.
  if (auto fit = root.find("flow_contract"); fit != root.end() && fit->second.is_object()) {
    const auto& flow = fit->second.as_object();
    if (auto k = flow.find("common_input"); k != flow.end()) {
      config.flow_contract.common_input = as_string_list(flow, "common_input");
    }
    if (auto k = flow.find("item_input"); k != flow.end()) {
      config.flow_contract.item_input = as_string_list(flow, "item_input");
    }
    if (auto k = flow.find("common_output"); k != flow.end()) {
      config.flow_contract.common_output = as_string_list(flow, "common_output");
    }
    if (auto k = flow.find("item_output"); k != flow.end()) {
      config.flow_contract.item_output = as_string_list(flow, "item_output");
    }
  }

  const auto& group = require_obj(root, "pipeline_group");
  for (const auto& [name, value] : group) {
    config.pipeline_group[name] = as_string_list(value.as_object(), "pipeline");
  }

  const auto& pipeline_config = require_obj(root, "pipeline_config");
  if (auto pit = pipeline_config.find("pipeline_map"); pit != pipeline_config.end()) {
    for (const auto& [name, value] : pit->second.as_object()) {
      config.pipeline_map[name] = as_string_list(value.as_object(), "pipeline");
    }
  }
  auto operators_it = pipeline_config.find("operators");
  if (operators_it == pipeline_config.end()) {
    throw ConfigError("missing required top-level field \"pipeline_config.operators\"");
  }
  for (const auto& [name, value] : operators_it->second.as_object()) {
    config.operators[name] = parse_operator(name, value);
  }
  apply_registry_traits(config);
  validate_config(config);
  return config;
}

ExpandedSequence expand_operator_sequence_with_subflows(const Config& config) {
  ExpandedSequence out;
  std::vector<std::string> root_entries;
  if (auto it = config.pipeline_group.find("main"); it != config.pipeline_group.end()) {
    root_entries = it->second;
  } else if (config.pipeline_group.size() == 1) {
    root_entries = config.pipeline_group.begin()->second;
  } else {
    throw ConfigError("pipeline_group must contain a \"main\" entry or exactly one entry");
  }
  std::set<std::string> visiting;
  std::set<std::string> seen;
  expand_entries(config, root_entries, "", out.sequence, out.op_to_subflow, visiting, seen);
  return out;
}

Request load_request_from_file(const std::string& path) {
  const auto root = parse_json(read_file(path)).as_object();
  Request request;
  if (auto it = root.find("common"); it != root.end()) {
    for (const auto& [key, value] : it->second.as_object()) {
      request.common[key] = value;
    }
  }
  if (auto it = root.find("items"); it != root.end()) {
    for (const auto& item : it->second.as_array()) {
      Variant::object_t row;
      for (const auto& [key, value] : item.as_object()) {
        row[key] = value;
      }
      request.items.push_back(std::move(row));
    }
  }
  return request;
}

std::string result_to_json(const Result& result) {
  Variant::object_t root;
  Variant::object_t common;
  for (const auto& [key, value] : result.common) {
    common[key] = value;
  }
  Variant::array_t items;
  for (const auto& row : result.items) {
    Variant::object_t obj;
    for (const auto& [key, value] : row) {
      obj[key] = value;
    }
    items.emplace_back(obj);
  }
  root["common"] = Variant(common);
  root["items"] = Variant(items);
  return dump_json(Variant(root));
}

}  // namespace pine
