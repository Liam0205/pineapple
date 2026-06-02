#include "pine/operator.hpp"

#include <memory>
#include <string>

#include "operators/_helpers.hpp"
#include "redis/connection_pool.hpp"
#include "redis/redis_client.hpp"

namespace pine {

class TransformRedisGetOp : public Operator, public ConcurrentSafe, public ResourceAware {
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
    if (auto it = params.find("fail_on_error"); it != params.end() && it->second.is_bool()) {
      fail_on_error_ = it->second.as_bool();
    }
    result_field_ = cfg.metadata.common_output.at(0);
    cache_hit_field_ = cfg.metadata.common_output.at(1);
    common_input_ = cfg.metadata.common_input;
  }

  void execute(const OperatorInput& input, OperatorOutput& out) override {
    // Borrow the connection pool from the redis_connection resource. A null
    // borrow (no provider, missing resource, or wrong handle type) degrades to
    // a cache miss, mirroring pine-go's borrowRedis ok=false path.
    auto conn = provider_
                    ? std::static_pointer_cast<redis::RedisConnResource>(provider_->borrow(resource_name_))
                    : nullptr;
    if (!conn) {
      out.set_common(cache_hit_field_, Variant(false));
      return;
    }

    // key_prefix is templatable (#74). When the DSL configured a {{field}}
    // marker the engine resolved it against this request's common frame
    // before execute; otherwise the raw init-time string is used.
    std::string prefix = key_prefix_;
    Variant resolved = input.templated_param("key_prefix");
    if (resolved.is_string()) {
      prefix = resolved.as_string();
    }
    std::string key = prefix + operators::build_key_suffix(input, common_input_);

    auto client = conn->acquire();
    redis::Client* cli = client.get();

    const char* cmd = (data_type_ == "set") ? "SMembers" : (data_type_ == "list") ? "LRange" : "Get";
    auto on_failure = [&](const std::string& msg) {
      out.set_warning("transform_redis_get: " + std::string(cmd) + "(" + key + "): " + msg);
      if (fail_on_error_) {
        throw ExecutionError("transform_redis_get: " + std::string(cmd) + "(" + key + "): " + msg);
      }
      out.set_common(cache_hit_field_, Variant(false));
    };

    if (!cli || !cli->connected()) {
      on_failure("connection failed");
      return;
    }

    try {
      if (data_type_ == "string") {
        auto val = cli->get(key);
        if (val && !val->empty()) {
          out.set_common(result_field_, Variant(*val));
          out.set_common(cache_hit_field_, Variant(true));
        } else {
          out.set_common(cache_hit_field_, Variant(false));
        }
      } else if (data_type_ == "set") {
        auto members = cli->smembers(key);
        if (!members.empty()) {
          Variant::array_t arr;
          for (auto& m : members) {
            arr.push_back(Variant(std::move(m)));
          }
          out.set_common(result_field_, Variant(std::move(arr)));
          out.set_common(cache_hit_field_, Variant(true));
        } else {
          out.set_common(cache_hit_field_, Variant(false));
        }
      } else if (data_type_ == "list") {
        auto vals = cli->lrange(key, 0, -1);
        if (!vals.empty()) {
          Variant::array_t arr;
          for (auto& v : vals) {
            arr.push_back(Variant(std::move(v)));
          }
          out.set_common(result_field_, Variant(std::move(arr)));
          out.set_common(cache_hit_field_, Variant(true));
        } else {
          out.set_common(cache_hit_field_, Variant(false));
        }
      } else {
        throw ExecutionError("transform_redis_get: unsupported data_type \"" + data_type_ + "\"");
      }
    } catch (const ExecutionError&) {
      throw;
    } catch (const std::exception& e) {
      on_failure(std::string(e.what()));
    }
  }

 private:
  const ResourceProvider* provider_ = nullptr;
  std::string op_name_;
  std::string resource_name_;
  std::string key_prefix_;
  std::string data_type_ = "string";
  bool fail_on_error_ = false;
  std::string result_field_;
  std::string cache_hit_field_;
  std::vector<std::string> common_input_;
};

static const OperatorSchema k_transform_redis_get_schema{
    .name = "transform_redis_get",
    .type = OpType::Transform,
    .description =
        "Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.",
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
            {"fail_on_error",
             {.type = "bool",
              .required = false,
              .default_value = Variant(false),
              .description =
                  "Return fatal error on Redis infrastructure failure instead of treating as cache miss."}},
        },
};
PINE_REGISTER_OPERATOR_T(TransformRedisGetOp, k_transform_redis_get_schema)

}  // namespace pine
