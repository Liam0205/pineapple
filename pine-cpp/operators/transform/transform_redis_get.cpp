#include "operators/_helpers.hpp"
#include "pine/operator.hpp"
#include "redis/connection_pool.hpp"
#include "redis/redis_client.hpp"

#include <memory>

namespace pine {

class TransformRedisGetOp : public Operator, public ConcurrentSafe {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        rp_ = operators::parse_redis_params(cfg);
        result_field_ = cfg.metadata.common_output.at(0);
        cache_hit_field_ = cfg.metadata.common_output.at(1);
        common_input_ = cfg.metadata.common_input;
    }
    void execute(const Frame& frame, OperatorOutput& out) override {
        if (rp_.host.empty()) {
            out.set_common(cache_hit_field_, JsonValue(false));
            return;
        }

        std::string key = rp_.key_prefix + operators::build_key_suffix(frame, common_input_);

        // Borrow a connection from the shared pool to avoid the full
        // getaddrinfo + connect + AUTH + SELECT round-trip on every
        // dispatch. P1-P4.
        std::unique_ptr<redis::Client> client;
        try {
            client = redis::shared_pool().acquire(rp_.host, rp_.port, rp_.password, rp_.db);
        } catch (const std::exception& e) {
            if (rp_.fail_on_error)
                throw ExecutionError("transform_redis_get: " + std::string(e.what()));
            out.set_warning("transform_redis_get: Get(" + key + "): " + std::string(e.what()));
            out.set_common(cache_hit_field_, JsonValue(false));
            return;
        }
        if (!client->connected()) {
            if (rp_.fail_on_error)
                throw ExecutionError("transform_redis_get: connection failed");
            out.set_warning("transform_redis_get: Get(" + key + "): connection failed");
            out.set_common(cache_hit_field_, JsonValue(false));
            return;
        }
        // Return-to-pool RAII guard: takes ownership of `client`, releases
        // back to the pool on every path. ConnectionPool::release drops
        // disconnected handles instead of recycling.
        struct PoolGuard {
            redis::ConnectionPool& pool;
            const std::string host;
            const int port;
            const std::string password;
            const int db;
            std::unique_ptr<redis::Client> c;
            ~PoolGuard() {
                if (c) pool.release(host, port, password, db, std::move(c));
            }
        };
        PoolGuard guard{redis::shared_pool(), rp_.host, rp_.port, rp_.password, rp_.db, std::move(client)};
        redis::Client* cli = guard.c.get();

        try {
            if (rp_.data_type == "string") {
                auto val = cli->get(key);
                if (val && !val->empty()) {
                    out.set_common(result_field_, JsonValue(*val));
                    out.set_common(cache_hit_field_, JsonValue(true));
                } else {
                    out.set_common(cache_hit_field_, JsonValue(false));
                }
            } else if (rp_.data_type == "set") {
                auto members = cli->smembers(key);
                if (!members.empty()) {
                    JsonValue::array_t arr;
                    for (auto& m : members) arr.push_back(JsonValue(std::move(m)));
                    out.set_common(result_field_, JsonValue(std::move(arr)));
                    out.set_common(cache_hit_field_, JsonValue(true));
                } else {
                    out.set_common(cache_hit_field_, JsonValue(false));
                }
            } else if (rp_.data_type == "list") {
                auto vals = cli->lrange(key, 0, -1);
                if (!vals.empty()) {
                    JsonValue::array_t arr;
                    for (auto& v : vals) arr.push_back(JsonValue(std::move(v)));
                    out.set_common(result_field_, JsonValue(std::move(arr)));
                    out.set_common(cache_hit_field_, JsonValue(true));
                } else {
                    out.set_common(cache_hit_field_, JsonValue(false));
                }
            } else {
                throw ExecutionError("transform_redis_get: unsupported data_type \"" + rp_.data_type + "\"");
            }
        } catch (const ExecutionError&) {
            throw;
        } catch (const std::exception& e) {
            if (rp_.fail_on_error)
                throw ExecutionError("transform_redis_get: " + std::string(e.what()));
            std::string cmd_name = (rp_.data_type == "set") ? "SMembers" : (rp_.data_type == "list") ? "LRange" : "Get";
            out.set_warning("transform_redis_get: " + cmd_name + "(" + key + "): " + std::string(e.what()));
            out.set_common(cache_hit_field_, JsonValue(false));
        }
    }
private:
    std::string op_name_;
    operators::RedisParams rp_;
    std::string result_field_;
    std::string cache_hit_field_;
    std::vector<std::string> common_input_;
};

static const OperatorSchema k_transform_redis_get_schema{
    .name = "transform_redis_get",
    .type = OpType::Transform,
    .description = "Generic Redis read operator. Reads a value by key and outputs the result and a cache-hit flag.",
    .params = {
        {"data_type", {.type = "string", .required = false, .default_value = JsonValue("string"),
                       .description = "Redis data type: \"set\", \"string\", or \"list\"."}},
        {"fail_on_error", {.type = "bool", .required = false, .default_value = JsonValue(false),
                           .description = "Return fatal error on Redis infrastructure failure instead of treating as cache miss."}},
        {"key_prefix", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                        .description = "Key prefix prepended to the suffix built from common_input fields."}},
        {"redis_addr", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                        .description = "Redis server address (host:port)."}},
        {"redis_db", {.type = "int", .required = false, .default_value = JsonValue(0.0),
                      .description = "Redis DB number."}},
        {"redis_password", {.type = "string", .required = false, .default_value = JsonValue(""),
                            .description = "Redis password."}},
    },
};
PINE_REGISTER_OPERATOR(k_transform_redis_get_schema,
    ([] { return std::make_unique<TransformRedisGetOp>(); }))

}  // namespace pine
