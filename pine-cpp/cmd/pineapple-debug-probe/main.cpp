// pineapple-debug-probe: embedded-API request-level debug parity probe
// (issue #219).
//
// Runs a fixed set of scenarios through Engine::execute, attaching (or not) a
// collecting debug::Emitter, and prints per scenario:
//   scenario=<name> error=<err or "nil"> items=[<per-item output values>]
//   scenario=<name> event=<operator>/<type>/<subflow>/<kind>/<data>
// Event lines are sorted, so concurrent nodes and data_parallel shards cannot
// reorder them. cross-validate Section 23 asserts stdout is byte-identical to
// pine-go's pine-debug-probe and pine-java's DebugProbe. Every value the probe
// prints is an integer, so they are formatted as such.
#include "pine/debug.hpp"
#include "pine/operator.hpp"
#include "pine/operator_input.hpp"
#include "pine/pine.hpp"

#include <algorithm>
#include <any>
#include <exception>
#include <iostream>
#include <mutex>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

const int kRequestKey = 0;

std::string int_text(const pine::Variant& v) {
  return v.is_number() ? std::to_string(static_cast<long long>(v.as_number())) : "<nil>";
}

// Copies item "value" into its configured field and, when a recorder is
// attached, emits the shard's item count and whether the request value
// survived node binding.
struct EmitOp : public pine::Operator, public pine::ConcurrentSafe {
  std::string field;

  void init(const pine::OperatorConfig& config) override {
    field = config.params.find("field")->as_string();
  }

  void execute(const pine::RequestContext& ctx, const pine::OperatorInput& input,
               pine::OperatorOutput& out) override {
    auto n = input.item_count();
    if (const auto* r = pine::debug::from_context(ctx)) {
      r->emit("items", pine::Variant(static_cast<int>(n)));
      const std::any* kept = ctx.value(&kRequestKey);
      bool ok = kept != nullptr && std::any_cast<std::string>(*kept) == "kept";
      r->emit("kept", pine::Variant(ok ? 1 : 0));
    }
    for (std::size_t i = 0; i < n; ++i) {
      out.set_item(static_cast<int>(i), field, input.item(i, "value"));
    }
  }
};

class Collector : public pine::debug::Emitter {
 public:
  void emit(const pine::debug::NodeMeta& meta, const std::string& kind, const pine::Variant& data) override {
    std::lock_guard<std::mutex> lk(mu_);
    lines.push_back(meta.operator_name + "/" + meta.type + "/" + meta.subflow + "/" + kind + "/" +
                    int_text(data));
  }

  std::vector<std::string> lines;

 private:
  std::mutex mu_;
};

class Throwing : public pine::debug::Emitter {
 public:
  void emit(const pine::debug::NodeMeta&, const std::string&, const pine::Variant&) override {
    throw std::runtime_error("emitter broke");
  }
};

std::string op_json(const std::string& name, const std::string& field, int data_parallel) {
  std::string dp = data_parallel > 1 ? ", \"data_parallel\": " + std::to_string(data_parallel) : "";
  return "\"" + name + "\": {\"type_name\": \"probe219_emit\", \"field\": \"" + field + "\"" + dp +
         ", \"$metadata\": {\"common_input\": [], \"common_output\": [], \"item_input\": [\"value\"],"
         " \"item_output\": [\"" +
         field + "\"]}}";
}

// "top" sits directly in the pipeline group, "mid" in SubFlow "outer", and
// "leaf" in SubFlow "inner", which is nested in "outer".
std::string nested_config() {
  return "{\"pipeline_config\": {\"operators\": {" + op_json("top", "a", 1) + ", " + op_json("mid", "b", 1) +
         ", " + op_json("leaf", "c", 1) +
         "},"
         " \"pipeline_map\": {\"outer\": {\"pipeline\": [\"mid\", \"inner\"]}, \"inner\": {\"pipeline\": "
         "[\"leaf\"]}}},"
         " \"pipeline_group\": {\"main\": {\"pipeline\": [\"top\", \"outer\"]}},"
         " \"flow_contract\": {\"common_input\": [], \"common_output\": [], \"item_input\": [\"value\"],"
         " \"item_output\": [\"a\", \"b\", \"c\"]}}";
}

std::string parallel_config() {
  return "{\"pipeline_config\": {\"operators\": {" + op_json("rank", "a", 3) +
         "},"
         " \"pipeline_map\": {\"scoring\": {\"pipeline\": [\"rank\"]}}},"
         " \"pipeline_group\": {\"main\": {\"pipeline\": [\"scoring\"]}},"
         " \"flow_contract\": {\"common_input\": [], \"common_output\": [], \"item_input\": [\"value\"],"
         " \"item_output\": [\"a\"]}}";
}

void run(const std::string& name, const std::string& config, int item_count, pine::debug::Emitter* emitter,
         Collector* c) {
  pine::Engine engine(pine::load_config_from_json(config));
  std::vector<std::string> fields;
  for (const char* f : {"a", "b", "c"}) {
    if (config.find(std::string("\"field\": \"") + f + "\"") != std::string::npos) {
      fields.emplace_back(f);
    }
  }
  pine::Request req;
  for (int i = 0; i < item_count; ++i) {
    pine::Variant::object_t item;
    item["value"] = pine::Variant(i + 1);
    req.items.push_back(std::move(item));
  }
  auto ctx =
      pine::debug::with_emitter(pine::RequestContext::with_value(pine::RequestContext::background(),
                                                                 &kRequestKey, std::any(std::string("kept"))),
                                emitter);
  std::string err = "nil";
  pine::TracedResult traced;
  try {
    engine.execute_traced_into(req, ctx, &traced);
  } catch (const std::exception& e) {
    err = e.what();
  }
  std::string values;
  for (std::size_t i = 0; i < traced.result.items.size(); ++i) {
    std::string vs;
    for (std::size_t j = 0; j < fields.size(); ++j) {
      const auto& item = traced.result.items[i];
      auto it = item.find(fields[j]);
      vs += (j ? ":" : "") + (it == item.end() ? std::string("<nil>") : int_text(it->second));
    }
    values += (i ? "," : "") + vs;
  }
  std::cout << "scenario=" << name << " error=" << err << " items=[" << values << "]\n";
  if (c != nullptr) {
    std::sort(c->lines.begin(), c->lines.end());
    for (const auto& line : c->lines) {
      std::cout << "scenario=" << name << " event=" << line << "\n";
    }
  }
  engine.close();
}

}  // namespace

int main() {
  pine::OperatorSchema schema;
  schema.name = "probe219_emit";
  schema.type = pine::OpType::Transform;
  schema.description = "probe: copies item value and emits request debug events";
  pine::ParamSchema field;
  field.type = "string";
  field.required = true;
  field.description = "item output field";
  schema.params.emplace("field", std::move(field));
  pine::register_operator_typed<EmitOp>(std::move(schema));

  // No emitter: no recorder reaches the operators, results are unchanged.
  run("no_emitter", nested_config(), 2, nullptr, nullptr);

  // NodeMeta carries the operator, its type_name and its direct SubFlow.
  Collector meta;
  run("node_meta", nested_config(), 2, &meta, &meta);

  // data_parallel: one event per shard, all with the node's meta.
  Collector parallel;
  run("data_parallel", parallel_config(), 6, &parallel, &parallel);

  // A throwing emitter is isolated: the business result is unchanged.
  Throwing throwing;
  run("panicking_emitter", parallel_config(), 6, &throwing, nullptr);
  return 0;
}
