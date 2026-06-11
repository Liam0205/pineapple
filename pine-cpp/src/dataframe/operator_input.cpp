#include "pine/operator_input.hpp"

#include "pine/frame.hpp"

#include <optional>
#include <set>

namespace pine {

OperatorInput::OperatorInput(const Frame& frame, const InputFieldSpec& spec)
    : frame_(&frame), spec_(&spec), cached_item_count_(frame.item_count()) {
}

Variant OperatorInput::common(const std::string& field) const {
  Variant v = frame_->common(field);
  if (!v.is_null()) {
    return v;
  }
  for (const auto& df : spec_->defaulted_common) {
    if (df.name == field) {
      return df.default_value;
    }
  }
  return Variant(nullptr);
}

Variant OperatorInput::item(std::size_t index, const std::string& field) const {
  if (index >= cached_item_count_) {
    return Variant(nullptr);
  }
  Variant v = frame_->item(index, field);
  if (!v.is_null()) {
    return v;
  }
  for (const auto& df : spec_->defaulted_item) {
    if (df.name == field) {
      return df.default_value;
    }
  }
  return Variant(nullptr);
}

std::vector<std::string> OperatorInput::common_keys() const {
  std::vector<std::string> keys;
  for (const auto& f : spec_->strict_common) {
    keys.push_back(f);
  }
  for (const auto& df : spec_->defaulted_common) {
    keys.push_back(df.name);
  }
  for (const auto& f : spec_->nullable_common) {
    keys.push_back(f);
  }
  return keys;
}

std::vector<std::string> OperatorInput::item_keys(std::size_t index) const {
  (void)index;
  std::vector<std::string> keys;
  for (const auto& f : spec_->strict_item) {
    keys.push_back(f);
  }
  for (const auto& df : spec_->defaulted_item) {
    keys.push_back(df.name);
  }
  for (const auto& f : spec_->nullable_item) {
    keys.push_back(f);
  }
  return keys;
}

const std::map<std::string, Variant>* OperatorInput::resources() const {
  return frame_->resources();
}

Variant OperatorInput::templated_param(const std::string& name) const {
  if (!templated_) {
    return Variant(nullptr);
  }
  auto it = templated_->find(name);
  if (it == templated_->end()) {
    return Variant(nullptr);
  }
  return it->second;
}

InputFieldSpec compute_input_field_spec(const OperatorConfig& config) {
  InputFieldSpec spec;

  // Engine-internal fields hidden from the operator's input view:
  //   * skip control fields (e.g. _if_*) — kept in metadata for DAG
  //     ordering only.
  //   * common_input_template source fields (#74) — surfaced via
  //     OperatorInput::templated_param, not OperatorInput::common.
  // Both are excluded unconditionally so legacy configs (skip fields
  // inline in common_input) and #74 configs (disjoint bucket lists)
  // produce the same operator-visible input.
  std::set<std::string> skip_set(config.skip.begin(), config.skip.end());
  skip_set.insert(config.metadata.common_input_skip.begin(), config.metadata.common_input_skip.end());
  skip_set.insert(config.metadata.common_input_template.begin(), config.metadata.common_input_template.end());
  std::set<std::string> strict_common_set(config.strict_common.begin(), config.strict_common.end());
  std::set<std::string> strict_item_set(config.strict_item.begin(), config.strict_item.end());

  for (const auto& field : config.metadata.common_input) {
    if (skip_set.count(field)) {
      continue;
    }
    auto def_it = config.common_defaults.find(field);
    if (def_it != config.common_defaults.end()) {
      spec.defaulted_common.push_back({field, def_it->second});
    } else if (strict_common_set.count(field)) {
      spec.strict_common.push_back(field);
    } else {
      spec.nullable_common.push_back(field);
    }
  }
  for (const auto& field : config.metadata.item_input) {
    auto def_it = config.item_defaults.find(field);
    if (def_it != config.item_defaults.end()) {
      spec.defaulted_item.push_back({field, def_it->second});
    } else if (strict_item_set.count(field)) {
      spec.strict_item.push_back(field);
    } else {
      spec.nullable_item.push_back(field);
    }
  }
  return spec;
}

OperatorInput build_operator_input(const Frame& frame, const std::string& op_name,
                                   const InputFieldSpec& spec) {
  // Validation order is part of the byte-exact error contract: when a config
  // violates both a common and an item field, all three runtimes must surface
  // the same first error. pine-go RowFrame/ColumnFrame.BuildInput and
  // pine-java DataFrame.buildInput check common (strict then nullable) before
  // item (strict then nullable), so we do too:
  //   strict_common → nullable_common → strict_item → nullable_item.
  std::optional<ExecutionError> err;

  // Window 1: strict + nullable common collapsed into one shared-lock window,
  // mirroring pine-go `f.mu.RLock(); defer f.mu.RUnlock()`. Validation errors
  // are captured outside the lambda and thrown after the RAII guard releases.
  frame.with_read_lock([&]() {
    for (const auto& field : spec.strict_common) {
      Variant v = frame.common_no_lock(field);
      if (v.is_null()) {
        err.emplace(op_name, "required field \"" + field + "\" is nil in common");
        return;
      }
    }

    for (const auto& field : spec.nullable_common) {
      if (!frame.has_common_no_lock(field)) {
        err.emplace(op_name, "required field \"" + field + "\" is missing in common");
        return;
      }
    }
  });
  if (err) {
    throw *err;
  }

  // Strict-item batch scan — for ColumnFrame a per-column bitmap walk inside
  // one lock, cheaper than a per-row item_has loop. It takes its own lock, so
  // it must sit outside the common/item windows (shared_mutex is non-recursive
  // and cannot nest), and ordered after common, before nullable item.
  if (!spec.strict_item.empty()) {
    auto [bad_field, bad_row] = frame.validate_strict_items(spec.strict_item);
    if (bad_row >= 0) {
      throw ExecutionError(
          op_name, "required field \"" + bad_field + "\" is nil on item[" + std::to_string(bad_row) + "]");
    }
  }

  // Window 2: nullable item — the hot path (N rows × M fields) still collapses
  // up to N×M separate shared_lock acquisitions into one window.
  frame.with_read_lock([&]() {
    const std::size_t n = frame.item_count_no_lock();
    for (const auto& field : spec.nullable_item) {
      for (std::size_t i = 0; i < n; ++i) {
        if (!frame.item_has_no_lock(i, field)) {
          err.emplace(op_name,
                      "required field \"" + field + "\" is missing on item[" + std::to_string(i) + "]");
          return;
        }
      }
    }
  });
  if (err) {
    throw *err;
  }

  // Return lazy proxy (PERF-1b)
  return OperatorInput(frame, spec);
}

}  // namespace pine
