#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

#include <algorithm>

namespace pine {

class TransformNormalizeOp : public Operator {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        field_ = cfg.metadata.item_input.at(0);
        out_field_ = cfg.metadata.item_output.at(0);
        item_defaults_ = cfg.item_defaults;
    }
    void execute(const Frame& frame, OperatorOutput& out) override {
        if (frame.item_count() == 0) return;
        std::vector<double> vals;
        vals.reserve(frame.item_count());
        for (std::size_t i = 0; i < frame.item_count(); ++i) {
            try {
                JsonValue v = frame.item(i, field_);
                if (v.is_null()) {
                    auto def = item_defaults_.find(field_);
                    if (def != item_defaults_.end()) v = def->second;
                    else throw operators::OperatorError("required field \"" + field_ + "\" is nil on item[" + std::to_string(i) + "]");
                }
                vals.push_back(operators::to_double(v));
            } catch (const operators::OperatorError& err) {
                throw ExecutionError("transform_normalize: item[" + std::to_string(i) + "]." + field_ + ": " + err.what());
            }
        }
        double minv = *std::min_element(vals.begin(), vals.end());
        double maxv = *std::max_element(vals.begin(), vals.end());
        double rng = maxv - minv;
        for (std::size_t i = 0; i < vals.size(); ++i) {
            double norm = (rng == 0.0) ? 0.0 : (vals[i] - minv) / rng;
            out.set_item(static_cast<int>(i), out_field_, JsonValue(norm));
        }
    }
private:
    std::string op_name_;
    std::string field_;
    std::string out_field_;
    std::map<std::string, JsonValue> item_defaults_;
};

static const OperatorSchema k_transform_normalize_schema{
    .name = "transform_normalize",
    .type = OpType::Transform,
    .description = "Normalizes a numeric item field using min-max scaling to [0, 1].",
    .params = {
        {"method", {.type = "string", .required = false, .default_value = JsonValue("min_max"),
                    .description = "Normalization method."}},
    },
};
PINE_REGISTER_OPERATOR_T(TransformNormalizeOp, k_transform_normalize_schema)

}  // namespace pine
