#include "pine/operator.hpp"

#include "lua/lua_bridge.hpp"
#include "lua/lua_pool.hpp"
#include "operators/_helpers.hpp"

namespace pine {

class TransformByLuaOp : public Operator, public ConcurrentSafe, public StatsProvider, public MetricsAware {
 public:
  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    const auto& params = cfg.params.as_object();
    auto script_it = params.find("lua_script");
    if (script_it == params.end() || !script_it->second.is_string()) {
      throw RegistryError(op_name_,
                          "lua: exactly one of function_for_item or function_for_common must be set");
    }
    lua_script_ = script_it->second.as_string();

    auto fi_it = params.find("function_for_item");
    auto fc_it = params.find("function_for_common");
    func_for_item_ = (fi_it != params.end() && fi_it->second.is_string()) ? fi_it->second.as_string() : "";
    func_for_common_ = (fc_it != params.end() && fc_it->second.is_string()) ? fc_it->second.as_string() : "";

    if (func_for_item_.empty() && func_for_common_.empty()) {
      throw RegistryError(op_name_,
                          "lua: exactly one of function_for_item or function_for_common must be set");
    }
    if (!func_for_item_.empty() && !func_for_common_.empty()) {
      throw RegistryError(op_name_, "lua: cannot set both function_for_item and function_for_common");
    }

    common_input_ = cfg.metadata.common_input;
    item_input_ = cfg.metadata.item_input;
    item_output_ = cfg.metadata.item_output;
    common_output_ = cfg.metadata.common_output;

    pool_ = std::make_unique<lua::StatePool>(lua_script_, op_name_);
  }

  void set_metrics_provider(metrics::Provider* provider) override {
    if (pool_) {
      pool_->set_metrics(provider, op_name_);
    }
  }

  std::map<std::string, int64_t> operator_stats() const override {
    if (pool_) {
      return pool_->stats_snapshot();
    }
    return {};
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    auto borrowed = pool_->borrow();
    lua::LuaVM& vm = *borrowed;

    if (!func_for_item_.empty()) {
      int nret = static_cast<int>(item_output_.size());
      for (const auto& field : common_input_) {
        vm.set_global(field, input.common(field));
      }
      for (std::size_t i = 0; i < input.item_count(); ++i) {
        for (const auto& field : item_input_) {
          vm.set_global(field, input.item(i, field));
        }
        auto results = vm.call_function(func_for_item_, nret, op_name_);
        for (int j = 0; j < nret; ++j) {
          out.set_item(static_cast<int>(i), item_output_[static_cast<std::size_t>(j)],
                       results[static_cast<std::size_t>(j)]);
        }
      }
    } else {
      int nret = static_cast<int>(common_output_.size());
      for (const auto& field : common_input_) {
        vm.set_global(field, input.common(field));
      }
      for (const auto& field : item_input_) {
        std::vector<JsonValue> column;
        column.reserve(input.item_count());
        for (std::size_t i = 0; i < input.item_count(); ++i) {
          column.push_back(input.item(i, field));
        }
        vm.set_global_table(field, column);
      }
      auto results = vm.call_function(func_for_common_, nret, op_name_);
      for (int j = 0; j < nret; ++j) {
        out.set_common(common_output_[static_cast<std::size_t>(j)], results[static_cast<std::size_t>(j)]);
      }
    }
  }

 private:
  std::string op_name_;
  std::string lua_script_;
  std::string func_for_item_;
  std::string func_for_common_;
  std::vector<std::string> common_input_;
  std::vector<std::string> item_input_;
  std::vector<std::string> item_output_;
  std::vector<std::string> common_output_;
  std::unique_ptr<lua::StatePool> pool_;
};

static const OperatorSchema k_transform_by_lua_schema{
    .name = "transform_by_lua",
    .type = OpType::Transform,
    .description = "Executes a Lua script for per-item or per-common computation.",
    .params =
        {
            {"function_for_common",
             {.type = "string",
              .required = false,
              .default_value = JsonValue(""),
              .description = "Function name to call once for all items."}},
            {"function_for_item",
             {.type = "string",
              .required = false,
              .default_value = JsonValue(""),
              .description = "Function name to call per item."}},
            {"lua_script",
             {.type = "string",
              .required = true,
              .default_value = JsonValue(nullptr),
              .description = "Lua source code defining the function to call."}},
        },
};
PINE_REGISTER_OPERATOR_T(TransformByLuaOp, k_transform_by_lua_schema)

}  // namespace pine
