#include "pine/operator.hpp"
#include "pine/template.hpp"

#include <memory>
#include <string>

#include "operators/_helpers.hpp"
#include "redis/connection_pool.hpp"
#include "redis/redis_client.hpp"

namespace pine {

class TransformRedisSetOp : public Operator, public ConcurrentSafe, public ResourceAware {
 public:
  void set_resource_provider(const ResourceProvider* provider) override {
    provider_ = provider;
  }

  void init(const OperatorConfig& cfg) override {
    op_name_ = cfg.name;
    const auto& params = cfg.params.as_object();
    if (auto it = params.find("resource_name"); it != params.end() && it->second.is_string()) {
      resource_name_ = it->second.as_string();
    }
    if (auto it = params.find("key_prefix"); it != params.end() && it->second.is_string()) {
      key_prefix_ = it->second.as_string();
    }
    if (auto it = params.find("data_type"); it != params.end() && it->second.is_string()) {
      data_type_ = it->second.as_string();
    }
    if (auto it = params.find("ttl"); it != params.end()) {
      const Variant& v = it->second;
      if (v.is_number()) {
        ttl_ = static_cast<int>(v.as_number());
      } else if (v.is_string()) {
        // Only a bare {{field}} marker survives here — engine resolves
        // it per-request at execute time. A non-marker string is hand-
        // edited garbage and must error out rather than silently
        // collapsing to TTL=0.
        if (!is_bare_marker(v.as_string())) {
          throw ExecutionError("transform_redis_set: ttl must be numeric");
        }
        ttl_ = 0;
      } else {
        throw ExecutionError("transform_redis_set: ttl must be numeric");
      }
    }
    if (auto it = params.find("fail_on_error"); it != params.end() && it->second.is_bool()) {
      fail_on_error_ = it->second.as_bool();
    }
    int n = static_cast<int>(cfg.metadata.common_input.size());
    if (n < 2) {
      throw ExecutionError(
          "transform_redis_set: common_input must have at least 2 fields (key fields + value field)");
    }
    common_input_ = cfg.metadata.common_input;
    key_fields_ = std::vector<std::string>(cfg.metadata.common_input.begin(),
                                           cfg.metadata.common_input.begin() + (n - 1));
    value_field_ = cfg.metadata.common_input.back();
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    // A null borrow (no provider, missing resource, or wrong handle type) is a
    // silent no-op, mirroring pine-go's borrowRedis ok=false path.
    auto conn = provider_
                    ? std::static_pointer_cast<redis::RedisConnResource>(provider_->borrow(resource_name_))
                    : nullptr;
    if (!conn) {
      return;
    }

    // key_prefix and ttl are both templatable (#74). When the DSL configured
    // a {{field}} marker the engine resolved it against this request's
    // common frame before execute; otherwise the init-time value is used.
    // The inner type checks are unreachable: build_templated_param_plan
    // rejects mismatched declared types and resolve_templated_params
    // normalizes through go_format_g / parse_int. Kept as defense in
    // depth — a missed cast would surface as the init-time value with a
    // literal {{field}} marker.
    std::string prefix = key_prefix_;
    Variant resolved_prefix = input.templated_param("key_prefix");
    if (resolved_prefix.is_string()) {
      prefix = resolved_prefix.as_string();
    }
    int ttl = ttl_;
    Variant resolved_ttl = input.templated_param("ttl");
    if (resolved_ttl.is_number()) {
      ttl = static_cast<int>(resolved_ttl.as_number());
    }

    std::string key = prefix + operators::build_key_suffix(input, key_fields_);
    Variant value = input.common(value_field_);

    auto write_failed = [&](const std::string& msg) {
      if (fail_on_error_) {
        throw ExecutionError("transform_redis_set: write key " + key + ": " + msg);
      }
      out.set_warning("transform_redis_set: write key " + key + ": " + msg);
    };

    auto client = conn->acquire();
    redis::Client* cli = client.get();
    if (!cli || !cli->connected()) {
      write_failed("connection failed");
      return;
    }

    try {
      if (data_type_ == "string") {
        if (!value.is_string()) {
          return;
        }
        cli->set(key, value.as_string(), ttl);
      } else if (data_type_ == "set") {
        auto members = operators::json_to_string_slice(value);
        if (members.empty()) {
          return;
        }
        std::vector<std::vector<std::string>> commands = {{"DEL", key}};
        std::vector<std::string> sadd_cmd = {"SADD", key};
        for (const auto& m : members) {
          sadd_cmd.push_back(m);
        }
        commands.push_back(std::move(sadd_cmd));
        if (ttl > 0) {
          commands.push_back({"EXPIRE", key, std::to_string(ttl)});
        }
        cli->write_multiexec(commands);
      } else if (data_type_ == "list") {
        auto members = operators::json_to_string_slice(value);
        if (members.empty()) {
          return;
        }
        std::vector<std::vector<std::string>> commands = {{"DEL", key}};
        std::vector<std::string> rpush_cmd = {"RPUSH", key};
        for (const auto& m : members) {
          rpush_cmd.push_back(m);
        }
        commands.push_back(std::move(rpush_cmd));
        if (ttl > 0) {
          commands.push_back({"EXPIRE", key, std::to_string(ttl)});
        }
        cli->write_multiexec(commands);
      } else {
        throw ExecutionError("transform_redis_set: unsupported data_type \"" + data_type_ + "\"");
      }
    } catch (const ExecutionError&) {
      throw;
    } catch (const std::exception& e) {
      write_failed(std::string(e.what()));
    }
  }

 private:
  const ResourceProvider* provider_ = nullptr;
  std::string op_name_;
  std::string resource_name_;
  std::string key_prefix_;
  std::string data_type_ = "string";
  int ttl_ = 0;
  bool fail_on_error_ = false;
  std::vector<std::string> common_input_;
  std::vector<std::string> key_fields_;
  std::string value_field_;
};

static const OperatorSchema k_transform_redis_set_schema{
    .name = "transform_redis_set",
    .type = OpType::Transform,
    .description = "Generic Redis write operator. Writes a value by key with optional TTL.",
    .params =
        {
            {"resource_name",
             {.type = "string",
              .required = true,
              .default_value = Variant(nullptr),
              .description = "Name of a redis_connection resource to borrow the client from."}},
            {"key_prefix",
             {.type = "string",
              .required = true,
              .default_value = Variant(nullptr),
              .description = "Key prefix prepended to the suffix built from common_input fields. "
                             "Supports {{field}} interpolation.",
              .templatable = true}},
            {"data_type",
             {.type = "string",
              .required = false,
              .default_value = Variant("string"),
              .description = "Redis data type: \"set\", \"string\", or \"list\"."}},
            {"ttl",
             {.type = "int",
              .required = false,
              .default_value = Variant(0.0),
              .description = "TTL in seconds. 0 means no expiry. Supports {{field}} interpolation.",
              .templatable = true}},
            {"fail_on_error",
             {.type = "bool",
              .required = false,
              .default_value = Variant(false),
              .description =
                  "Return fatal error on Redis infrastructure failure instead of logging and continuing."}},
        },
};
PINE_REGISTER_OPERATOR_T(TransformRedisSetOp, k_transform_redis_set_schema)

}  // namespace pine
