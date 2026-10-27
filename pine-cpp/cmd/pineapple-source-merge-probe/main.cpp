// pineapple-source-merge-probe: cross-runtime probe for the `_source` rewrite
// exemption of the declared-output contract (issue #224).
//
// A merge keeps the first item per "id" and rewrites the kept item's
// `_source` with the sorted, comma-joined tags of its duplicates — the shape
// the DSL cannot declare, because it rejects underscore-prefixed outputs.
// Each scenario prints:
//   scenario=<name> error=<err or "nil"> items=[<id>:<_source>;...]
// Items are printed only when the run succeeds. cross-validate Section 24
// asserts stdout is byte-identical to pine-go's pine-source-merge-probe and
// pine-java's SourceMergeProbe.
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <algorithm>
#include <exception>
#include <iostream>
#include <map>
#include <string>
#include <utility>
#include <vector>

namespace {

std::string string_or_empty(const pine::Variant& v) {
  return v.is_string() ? v.as_string() : std::string();
}

// Dedups by "id" and rewrites the kept item's `_source`. With kExtra it also
// writes an undeclared "extra" field on every kept item.
template <bool kExtra>
struct MergeOp : public pine::Operator {
  void init(const pine::OperatorConfig&) override {
  }

  void execute(const pine::RequestContext& /*ctx*/, const pine::OperatorInput& input,
               pine::OperatorOutput& out) override {
    std::map<std::string, int> kept;
    std::map<int, std::vector<std::string>> tags;
    for (std::size_t i = 0; i < input.item_count(); ++i) {
      const int idx = static_cast<int>(i);
      const std::string key = string_or_empty(input.item(i, "id"));
      // "" when _source is not projected
      const std::string tag = string_or_empty(input.item(i, "_source"));
      auto it = kept.find(key);
      if (it != kept.end()) {
        tags[it->second].push_back(tag);
        out.remove_item(idx);
        continue;
      }
      kept[key] = idx;
      tags[idx].push_back(tag);
    }
    for (auto& [idx, list] : tags) {
      if (list.size() > 1) {
        std::sort(list.begin(), list.end());
        std::string joined;
        for (const auto& t : list) {
          joined += (joined.empty() ? "" : ",") + t;
        }
        out.set_item(idx, "_source", pine::Variant(joined));
      }
      if (kExtra) {
        out.set_item(idx, "extra", pine::Variant(1.0));
      }
    }
  }
};

// Carries the row-set markers the exemption requires.
template <bool kExtra>
struct RowSetMerge : public MergeOp<kExtra>, public pine::ConsumesRowSet, public pine::MutatesRowSet {};

std::string config(const std::string& type_name, const std::string& item_input) {
  return "{\"pipeline_config\": {\"operators\": {\"m\": {\"type_name\": \"" + type_name +
         "\","
         " \"$metadata\": {\"common_input\": [], \"common_output\": [], \"item_input\": " +
         item_input +
         ", \"item_output\": []}}}, \"pipeline_map\": {}},"
         " \"pipeline_group\": {\"main\": {\"pipeline\": [\"m\"]}},"
         " \"flow_contract\": {\"common_input\": [], \"common_output\": [],"
         " \"item_input\": [\"id\", \"_source\"], \"item_output\": [\"id\", \"_source\"]}}";
}

void run(const std::string& name, const std::string& cfg) {
  pine::Engine engine(pine::load_config_from_json(cfg));
  pine::Request req;
  for (const auto& [id, src] :
       std::vector<std::pair<std::string, std::string>>{{"x", "b"}, {"x", "a"}, {"y", "c"}}) {
    pine::Variant::object_t item;
    item["id"] = pine::Variant(id);
    item["_source"] = pine::Variant(src);
    req.items.push_back(std::move(item));
  }
  std::string err = "nil";
  std::string values;
  try {
    auto res = engine.execute(req);
    for (std::size_t i = 0; i < res.items.size(); ++i) {
      values += (i ? ";" : "") + string_or_empty(res.items[i].at("id")) + ":" +
                string_or_empty(res.items[i].at("_source"));
    }
  } catch (const std::exception& e) {
    err = e.what();
  }
  std::cout << "scenario=" << name << " error=" << err << " items=[" << values << "]\n";
  engine.close();
}

pine::OperatorSchema merge_schema(const std::string& name, const std::string& description) {
  pine::OperatorSchema schema;
  schema.name = name;
  schema.type = pine::OpType::Merge;
  schema.description = description;
  return schema;
}

}  // namespace

int main() {
  pine::register_operator_typed<RowSetMerge<false>>(
      merge_schema("probe224_merge", "probe: row-set merge rewriting _source"));
  pine::register_operator_typed<RowSetMerge<true>>(
      merge_schema("probe224_merge_extra", "probe: row-set merge also writing an undeclared field"));
  pine::register_operator_typed<MergeOp<false>>(
      merge_schema("probe224_merge_plain", "probe: merge without row-set markers"));

  // MutatesRowSet and reads _source: the rewrite is exempt.
  run("exempt", config("probe224_merge", "[\"id\", \"_source\"]"));
  // Other undeclared writes are still reported, without _source.
  run("exempt_other_undeclared", config("probe224_merge_extra", "[\"id\", \"_source\"]"));
  // Does not read _source: no exemption.
  run("no_source_input", config("probe224_merge", "[\"id\"]"));
  // Not MutatesRowSet: no exemption.
  run("no_row_set_markers", config("probe224_merge_plain", "[\"id\", \"_source\"]"));
  return 0;
}
