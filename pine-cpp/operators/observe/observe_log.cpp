#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

#include <iostream>

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
        JsonValue::object_t snapshot;
        if (!common_input_.empty()) {
            JsonValue::object_t common;
            for (const auto& k : common_input_) {
                common[k] = input.common(k);
            }
            snapshot["common"] = JsonValue(std::move(common));
        }
        if (!item_input_.empty() && input.item_count() > 0) {
            JsonValue::array_t items;
            items.reserve(input.item_count());
            for (std::size_t i = 0; i < input.item_count(); ++i) {
                JsonValue::object_t row;
                for (const auto& k : item_input_) {
                    row[k] = input.item(i, k);
                }
                items.push_back(JsonValue(std::move(row)));
            }
            snapshot["items"] = JsonValue(std::move(items));
        }

        std::string data = dump_json(JsonValue(snapshot), 0);
        while (!data.empty() && data.back() == '\n') data.pop_back();

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
    .description = "Reads declared input fields and writes them to Go standard log. This is a read-only operator: it produces no output fields and does not modify the DataFrame. It is exempt from dead-code detection.",
    .params = {
        {"log_prefix", {.type = "string", .required = false, .default_value = JsonValue(""),
                        .description = "Prefix prepended to each log line."}},
    },
};
PINE_REGISTER_OPERATOR_T(ObserveLogOp, k_observe_log_schema)

}  // namespace pine
