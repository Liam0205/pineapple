#include "pine/operator.hpp"

#include <iostream>

#include "operators/_helpers.hpp"

namespace pine {

class ObserveLogOp : public Operator {
 public:
  void init(const OperatorConfig& cfg) override {
    common_input_ = cfg.metadata.common_input;
    item_input_ = cfg.metadata.item_input;
    const auto& params = cfg.params.as_object();
    auto it = params.find("log_prefix");
    if (it != params.end() && !it->second.is_null()) {
      prefix_ = it->second.as_string();
    }
  }

  void execute(const OperatorInput& input, OperatorOutput& /*out*/) override {
    Variant::object_t snapshot;
    if (!common_input_.empty()) {
      Variant::object_t common;
      for (const auto& k : common_input_) {
        common[k] = input.common(k);
      }
      snapshot["common"] = Variant(std::move(common));
    }
    if (!item_input_.empty() && input.item_count() > 0) {
      Variant::array_t items;
      items.reserve(input.item_count());
      for (std::size_t i = 0; i < input.item_count(); ++i) {
        Variant::object_t row;
        for (const auto& k : item_input_) {
          row[k] = input.item(i, k);
        }
        items.push_back(Variant(std::move(row)));
      }
      snapshot["items"] = Variant(std::move(items));
    }

    std::string data = dump_json(Variant(snapshot), 0);
    while (!data.empty() && data.back() == '\n') {
      data.pop_back();
    }

    if (!prefix_.empty()) {
      std::cerr << "[observe_log] " << prefix_ << " " << data << "\n";
    } else {
      std::cerr << "[observe_log] " << data << "\n";
    }
  }

 private:
  std::string prefix_;
  std::vector<std::string> common_input_;
  std::vector<std::string> item_input_;
};

static const OperatorSchema k_observe_log_schema{
    .name = "observe_log",
    .type = OpType::Observe,
    .description =
        "Reads declared input fields and writes them to Go standard log. This is a read-only operator: it "
        "produces no output fields and does not modify the DataFrame. It is exempt from dead-code detection.",
    .params =
        {
            {"log_prefix",
             {.type = "string",
              .required = false,
              .default_value = Variant(""),
              .description = "Prefix prepended to each log line."}},
        },
};
PINE_REGISTER_OPERATOR_T(ObserveLogOp, k_observe_log_schema)

}  // namespace pine
