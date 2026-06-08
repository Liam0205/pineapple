#include "pine/operator_input.hpp"

#include "pine/frame.hpp"

#include <set>

namespace pine {

OperatorInput::OperatorInput(const Frame& frame, const InputFieldSpec& spec)
    : frame_(&frame), spec_(&spec), cached_item_count_(frame.item_count()) {
}

Variant OperatorInput::common(const std::string& field) const {
  // The proxy reads through Frame::common_no_lock by design. The engine
  // (run_dag → dispatch_with_recovery) wraps the entire build_input +
  // execute window in frame.with_read_lock, so lock acquisition for the
  // many per-row reads inside an operator collapses to a single
  // shared_lock per operator dispatch — mirroring pine-go RowFrame
  // BuildInput's `f.mu.RLock(); defer f.mu.RUnlock(); ...` window.
  // Direct callers outside the engine (unit tests, integration shims)
  // must satisfy the same precondition.
  Variant v = frame_->common_no_lock(field);
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
  // See common(): assumes caller holds frame.read_lock() — engine wraps
  // the full operator dispatch window in frame.with_read_lock.
  Variant v = frame_->item_no_lock(index, field);
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
  // CONTRACT: caller wraps build_operator_input + dispatch_with_recovery
  // in a single frame.with_read_lock(). All reads here go through
  // *_no_lock — re-entering the locking variants would deadlock on
  // std::shared_mutex (not recursive). The strict_item batch scan
  // also uses the no_lock variant.
  //
  // Stage history (chore/bench_and_doc):
  //   - stage-1 (eab4415): nested an inner frame.with_read_lock around
  //     these loops. Two locking windows per op (build_input + dispatch).
  //   - stage-2 (9f7db78): added the engine-side dispatch with_read_lock.
  //   - stage-3 (this commit): collapse the two windows into one. The
  //     engine takes a single read_lock, build_operator_input runs
  //     lock-free beneath it. On a clean machine 1k req × 20 conc,
  //     calibrated_2c4g shows stage-3 ≈ stage-2 (234 vs 234 QPS, both
  //     +4% over the merge_dedup-only baseline of 225). Stage-3 keeps
  //     the win with one fewer lambda.
  if (!spec.strict_item.empty()) {
    auto [bad_field, bad_row] = frame.validate_strict_items_no_lock(spec.strict_item);
    if (bad_row >= 0) {
      throw ExecutionError(
          op_name, "required field \"" + bad_field + "\" is nil on item[" + std::to_string(bad_row) + "]");
    }
  }

  for (const auto& field : spec.strict_common) {
    Variant v = frame.common_no_lock(field);
    if (v.is_null()) {
      throw ExecutionError(op_name, "required field \"" + field + "\" is nil in common");
    }
  }

  for (const auto& field : spec.nullable_common) {
    if (!frame.has_common_no_lock(field)) {
      throw ExecutionError(op_name, "required field \"" + field + "\" is missing in common");
    }
  }

  const std::size_t n = frame.item_count_no_lock();
  for (const auto& field : spec.nullable_item) {
    for (std::size_t i = 0; i < n; ++i) {
      if (!frame.item_has_no_lock(i, field)) {
        throw ExecutionError(
            op_name, "required field \"" + field + "\" is missing on item[" + std::to_string(i) + "]");
      }
    }
  }

  // Return lazy proxy (PERF-1b)
  return OperatorInput(frame, spec);
}

}  // namespace pine
