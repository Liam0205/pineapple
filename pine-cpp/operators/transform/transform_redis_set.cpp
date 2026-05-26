#include "operators/_helpers.hpp"
#include "pine/operator.hpp"
#include "redis/connection_pool.hpp"
#include "redis/redis_client.hpp"

#include <memory>

namespace pine {

class TransformRedisSetOp : public Operator, public ConcurrentSafe {
public:
    void init(const OperatorConfig& cfg) override {
        op_name_ = cfg.name;
        rp_ = operators::parse_redis_params(cfg);
        int n = static_cast<int>(cfg.metadata.common_input.size());
        if (n < 2)
            throw ExecutionError("transform_redis_set: common_input must have at least 2 fields (key fields + value field)");
        common_input_ = cfg.metadata.common_input;
        key_fields_ = std::vector<std::string>(cfg.metadata.common_input.begin(), cfg.metadata.common_input.begin() + (n - 1));
        value_field_ = cfg.metadata.common_input.back();
    }
    void execute(const Frame& frame, OperatorOutput& out) override {
        if (rp_.host.empty()) return;

        std::string key = rp_.key_prefix + operators::build_key_suffix(frame, key_fields_);
        JsonValue value = frame.common(value_field_);

        // Borrow from the shared pool (P1-P4). See transform_redis_get for
        // pool semantics; same RAII guard pattern.
        std::unique_ptr<redis::Client> client;
        try {
            client = redis::shared_pool().acquire(rp_.host, rp_.port, rp_.password, rp_.db);
        } catch (const std::exception& e) {
            if (rp_.fail_on_error)
                throw ExecutionError("transform_redis_set: write key " + key + ": " + e.what());
            out.set_warning("transform_redis_set: write key " + key + ": " + std::string(e.what()));
            return;
        }
        if (!client->connected()) {
            if (rp_.fail_on_error)
                throw ExecutionError("transform_redis_set: write key " + key + ": connection failed");
            out.set_warning("transform_redis_set: write key " + key + ": connection failed");
            return;
        }
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
                if (!value.is_string()) return;
                cli->set(key, value.as_string(), rp_.ttl);
            } else if (rp_.data_type == "set") {
                auto members = operators::json_to_string_slice(value);
                if (members.empty()) return;
                std::vector<std::vector<std::string>> commands = {{"DEL", key}};
                std::vector<std::string> sadd_cmd = {"SADD", key};
                for (const auto& m : members) sadd_cmd.push_back(m);
                commands.push_back(std::move(sadd_cmd));
                if (rp_.ttl > 0) {
                    commands.push_back({"EXPIRE", key, std::to_string(rp_.ttl)});
                }
                cli->write_multiexec(commands);
            } else if (rp_.data_type == "list") {
                auto members = operators::json_to_string_slice(value);
                if (members.empty()) return;
                std::vector<std::vector<std::string>> commands = {{"DEL", key}};
                std::vector<std::string> rpush_cmd = {"RPUSH", key};
                for (const auto& m : members) rpush_cmd.push_back(m);
                commands.push_back(std::move(rpush_cmd));
                if (rp_.ttl > 0) {
                    commands.push_back({"EXPIRE", key, std::to_string(rp_.ttl)});
                }
                cli->write_multiexec(commands);
            } else {
                throw ExecutionError("transform_redis_set: unsupported data_type \"" + rp_.data_type + "\"");
            }
        } catch (const ExecutionError&) {
            throw;
        } catch (const std::exception& e) {
            if (rp_.fail_on_error)
                throw ExecutionError("transform_redis_set: write key " + key + ": " + e.what());
            out.set_warning("transform_redis_set: write key " + key + ": " + std::string(e.what()));
        }
    }
private:
    std::string op_name_;
    operators::RedisParams rp_;
    std::vector<std::string> common_input_;
    std::vector<std::string> key_fields_;
    std::string value_field_;
};

static const OperatorSchema k_transform_redis_set_schema{
    .name = "transform_redis_set",
    .type = OpType::Transform,
    .description = "Generic Redis write operator. Writes a value by key with optional TTL.",
    .params = {
        {"data_type", {.type = "string", .required = false, .default_value = JsonValue("string"),
                       .description = "Redis data type: \"set\", \"string\", or \"list\"."}},
        {"fail_on_error", {.type = "bool", .required = false, .default_value = JsonValue(false),
                           .description = "Return fatal error on Redis infrastructure failure instead of logging and continuing."}},
        {"key_prefix", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                        .description = "Key prefix prepended to the suffix built from common_input fields."}},
        {"redis_addr", {.type = "string", .required = true, .default_value = JsonValue(nullptr),
                        .description = "Redis server address (host:port)."}},
        {"redis_db", {.type = "int", .required = false, .default_value = JsonValue(0.0),
                      .description = "Redis DB number."}},
        {"redis_password", {.type = "string", .required = false, .default_value = JsonValue(""),
                            .description = "Redis password."}},
        {"ttl", {.type = "int", .required = false, .default_value = JsonValue(0.0),
                 .description = "TTL in seconds. 0 means no expiry."}},
    },
};
PINE_REGISTER_OPERATOR_T(TransformRedisSetOp, k_transform_redis_set_schema)

}  // namespace pine
