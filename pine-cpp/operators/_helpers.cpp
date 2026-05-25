#include "operators/_helpers.hpp"

#include <algorithm>
#include <charconv>
#include <cmath>
#include <cstring>

namespace pine {
namespace operators {

double to_double(const JsonValue& value) {
    if (value.is_bool()) throw OperatorError("cannot convert bool to float64");
    if (value.is_number()) return value.as_number();
    if (value.is_null()) throw OperatorError("cannot convert <nil> to float64");
    if (value.is_string()) throw OperatorError("cannot convert string to float64");
    if (value.is_array()) throw OperatorError("cannot convert []interface {} to float64");
    throw OperatorError("cannot convert map[string]interface {} to float64");
}

JsonValue require_common(const Frame& frame, const OperatorConfig& op, const std::string& field) {
    JsonValue v = frame.common(field);
    if (!v.is_null()) return v;
    if (auto def = op.common_defaults.find(field); def != op.common_defaults.end()) return def->second;
    throw ExecutionError("required field \"" + field + "\" is nil in common");
}

JsonValue require_item(const Frame& frame, const OperatorConfig& op, std::size_t index, const std::string& field) {
    JsonValue v = frame.item(index, field);
    if (!v.is_null()) return v;
    if (auto def = op.item_defaults.find(field); def != op.item_defaults.end()) return def->second;
    throw ExecutionError("required field \"" + field + "\" is nil on item[" + std::to_string(index) + "]");
}

std::string go_format_g(double d) {
    if (std::isnan(d)) return "NaN";
    if (std::isinf(d)) return d < 0 ? "-Inf" : "+Inf";
    if (d == 0.0) return std::signbit(d) ? "-0" : "0";

    char buf[64];
    auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d, std::chars_format::scientific);
    std::string s(buf, ptr);

    std::size_t i = 0;
    bool neg = false;
    if (s[i] == '-') { neg = true; ++i; }
    std::string mantissa;
    while (i < s.size() && s[i] != 'e' && s[i] != 'E') {
        if (s[i] != '.') mantissa.push_back(s[i]);
        ++i;
    }
    ++i;  // skip 'e'
    bool exp_neg = false;
    if (i < s.size() && (s[i] == '+' || s[i] == '-')) {
        exp_neg = (s[i] == '-');
        ++i;
    }
    int exp_val = 0;
    while (i < s.size()) { exp_val = exp_val * 10 + (s[i] - '0'); ++i; }
    if (exp_neg) exp_val = -exp_val;

    int nd = static_cast<int>(mantissa.size());
    std::string result;
    if (neg) result += '-';

    if (exp_val < -4 || exp_val >= 6) {
        result += mantissa[0];
        if (nd > 1) {
            result += '.';
            result.append(mantissa, 1, std::string::npos);
        }
        result += 'e';
        int e = exp_val;
        if (e >= 0) result += '+';
        else { result += '-'; e = -e; }
        if (e < 10) result += '0';
        result += std::to_string(e);
    } else {
        int dp_pos = exp_val + 1;
        if (dp_pos <= 0) {
            result += "0.";
            for (int k = 0; k < -dp_pos; ++k) result += '0';
            result += mantissa;
        } else if (dp_pos >= nd) {
            result += mantissa;
            for (int k = 0; k < dp_pos - nd; ++k) result += '0';
        } else {
            result.append(mantissa, 0, dp_pos);
            result += '.';
            result.append(mantissa, dp_pos, std::string::npos);
        }
    }
    return result;
}

std::string sprint_value(const JsonValue& v) {
    if (v.is_null()) return "<nil>";
    if (v.is_bool()) return v.as_bool() ? "true" : "false";
    if (v.is_number()) return go_format_g(v.as_number());
    if (v.is_string()) return v.as_string();
    return "<complex>";
}

std::string any_to_string(const JsonValue& v) {
    if (v.is_null()) return "";
    if (v.is_string()) return v.as_string();
    if (v.is_number()) return go_format_g(v.as_number());
    if (v.is_bool()) return v.as_bool() ? "true" : "false";
    return "";
}

std::string dedup_key(const JsonValue& v) {
    if (v.is_null()) return "N:";
    if (v.is_bool()) return v.as_bool() ? "B:1" : "B:0";
    if (v.is_number()) return "F:" + go_format_g(v.as_number());
    if (v.is_string()) return "S:" + v.as_string();
    return "O:" + sprint_value(v);
}

std::string build_key_suffix(const Frame& frame, const std::vector<std::string>& fields) {
    if (fields.empty()) return "";
    std::string result;
    for (std::size_t i = 0; i < fields.size(); ++i) {
        if (i > 0) result += ':';
        result += sprint_value(frame.common(fields[i]));
    }
    return result;
}

std::vector<std::string> json_to_string_slice(const JsonValue& v) {
    std::vector<std::string> out;
    if (!v.is_array()) return out;
    for (const auto& item : v.as_array()) {
        out.push_back(sprint_value(item));
    }
    return out;
}

RedisParams parse_redis_params(const OperatorConfig& op) {
    RedisParams rp;
    const auto& params = op.params.as_object();
    auto addr_it = params.find("redis_addr");
    if (addr_it != params.end() && addr_it->second.is_string()) {
        const auto& addr = addr_it->second.as_string();
        auto colon = addr.rfind(':');
        if (colon != std::string::npos) {
            rp.host = addr.substr(0, colon);
            rp.port = std::stoi(addr.substr(colon + 1));
        } else {
            rp.host = addr;
        }
    }
    if (auto it = params.find("redis_password"); it != params.end() && it->second.is_string())
        rp.password = it->second.as_string();
    if (auto it = params.find("redis_db"); it != params.end() && it->second.is_number())
        rp.db = static_cast<int>(it->second.as_number());
    if (auto it = params.find("key_prefix"); it != params.end() && it->second.is_string())
        rp.key_prefix = it->second.as_string();
    if (auto it = params.find("data_type"); it != params.end() && it->second.is_string())
        rp.data_type = it->second.as_string();
    if (auto it = params.find("ttl"); it != params.end() && it->second.is_number())
        rp.ttl = static_cast<int>(it->second.as_number());
    if (auto it = params.find("fail_on_error"); it != params.end() && it->second.is_bool())
        rp.fail_on_error = it->second.as_bool();
    return rp;
}

}  // namespace operators
}  // namespace pine
