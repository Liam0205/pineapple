#pragma once
#include "pine/pine.hpp"
#include "pine/column_frame.hpp"

#include <cstdint>
#include <string>
#include <vector>

namespace pine {
namespace operators {

using Frame = ColumnFrame;

// Exception type for operator-internal errors (converted to ExecutionError by caller).
class OperatorError : public std::runtime_error {
public:
    using std::runtime_error::runtime_error;
};

double to_double(const JsonValue& value);

JsonValue require_common(const Frame& frame, const OperatorConfig& op, const std::string& field);
JsonValue require_item(const Frame& frame, const OperatorConfig& op, std::size_t index, const std::string& field);

std::string go_format_g(double d);
std::string sprint_value(const JsonValue& v);
std::string any_to_string(const JsonValue& v);
std::string dedup_key(const JsonValue& v);
std::string build_key_suffix(const Frame& frame, const std::vector<std::string>& fields);
std::vector<std::string> json_to_string_slice(const JsonValue& v);

struct RedisParams {
    std::string host;
    int port = 6379;
    std::string password;
    int db = 0;
    std::string key_prefix;
    std::string data_type = "string";
    int ttl = 0;
    bool fail_on_error = false;
};

RedisParams parse_redis_params(const OperatorConfig& op);

}  // namespace operators
}  // namespace pine
