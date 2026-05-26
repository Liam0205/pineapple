#include "operators/_helpers.hpp"
#include "pine/operator.hpp"

#include <algorithm>
#include <charconv>
#include <cstdint>

namespace pine {

namespace {

uint64_t fnv64a(const std::string& s) {
    uint64_t hash = 14695981039346656037ULL;
    for (unsigned char c : s) {
        hash ^= c;
        hash *= 1099511628211ULL;
    }
    return hash;
}

uint64_t parse_uint64(const std::string& s) {
    uint64_t result = 0;
    std::from_chars(s.data(), s.data() + s.size(), result);
    return result;
}

}  // namespace

class ReorderShuffleBySaltOp : public Operator, public ConsumesRowSet, public MutatesRowSet {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        common_inputs_ = cfg.metadata.common_input;
        item_field_ = cfg.metadata.item_input.at(0);
    }
    void execute(const Frame& frame, OperatorOutput& out) override {
        if (frame.item_count() == 0) return;
        std::string salt;
        for (std::size_t i = 0; i < common_inputs_.size(); ++i) {
            if (i > 0) salt += '|';
            salt += operators::any_to_string(frame.common(common_inputs_[i]));
        }
        salt += '|';
        struct Ranked { std::size_t idx; double r; uint64_t id; };
        std::vector<Ranked> ranked;
        ranked.reserve(frame.item_count());
        for (std::size_t i = 0; i < frame.item_count(); ++i) {
            std::string item_val = operators::any_to_string(frame.item(i, item_field_));
            uint64_t h = fnv64a(salt + item_val);
            double r = static_cast<double>(h) / (static_cast<double>(UINT64_MAX) + 1.0);
            ranked.push_back({i, r, parse_uint64(item_val)});
        }
        std::stable_sort(ranked.begin(), ranked.end(), [](const Ranked& a, const Ranked& b) {
            if (a.r != b.r) return a.r < b.r;
            return a.id < b.id;
        });
        std::vector<int> order;
        order.reserve(ranked.size());
        for (const auto& r : ranked) order.push_back(static_cast<int>(r.idx));
        out.set_item_order(std::move(order));
    }
private:
    std::string op_name_;
    std::vector<std::string> common_inputs_;
    std::string item_field_;
};

static const OperatorSchema k_reorder_shuffle_by_salt_schema{
    .name = "reorder_shuffle_by_salt",
    .type = OpType::Reorder,
    .description = "Deterministic hash-based shuffle using a caller-provided salt.",
    .params = {},
};
PINE_REGISTER_OPERATOR(k_reorder_shuffle_by_salt_schema,
    ([] { return std::make_unique<ReorderShuffleBySaltOp>(); }))

}  // namespace pine
