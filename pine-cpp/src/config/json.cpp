#include "pine/pine.hpp"

#include <cctype>
#include <charconv>
#include <cmath>
#include <cstdint>
#include <fstream>
#include <iomanip>
#include <sstream>

namespace pine {

JsonValue::JsonValue() : value_(nullptr) {}
JsonValue::JsonValue(std::nullptr_t) : value_(nullptr) {}
JsonValue::JsonValue(bool value) : value_(value) {}
JsonValue::JsonValue(double value) : value_(value) {}
JsonValue::JsonValue(int value) : value_(static_cast<double>(value)) {}
JsonValue::JsonValue(std::string value) : value_(std::move(value)) {}
JsonValue::JsonValue(const char* value) : value_(std::string(value)) {}
JsonValue::JsonValue(array_t value) : value_(std::move(value)) {}
JsonValue::JsonValue(object_t value) : value_(std::move(value)) {}

bool JsonValue::is_null() const { return std::holds_alternative<std::nullptr_t>(value_); }
bool JsonValue::is_bool() const { return std::holds_alternative<bool>(value_); }
bool JsonValue::is_number() const { return std::holds_alternative<double>(value_); }
bool JsonValue::is_string() const { return std::holds_alternative<std::string>(value_); }
bool JsonValue::is_array() const { return std::holds_alternative<array_t>(value_); }
bool JsonValue::is_object() const { return std::holds_alternative<object_t>(value_); }

bool JsonValue::as_bool() const {
    if (!is_bool()) throw ConfigError("JSON value is not bool");
    return std::get<bool>(value_);
}

double JsonValue::as_number() const {
    if (!is_number()) throw ConfigError("JSON value is not number");
    return std::get<double>(value_);
}

const std::string& JsonValue::as_string() const {
    if (!is_string()) throw ConfigError("JSON value is not string");
    return std::get<std::string>(value_);
}

const JsonValue::array_t& JsonValue::as_array() const {
    if (!is_array()) throw ConfigError("JSON value is not array");
    return std::get<array_t>(value_);
}

const JsonValue::object_t& JsonValue::as_object() const {
    if (!is_object()) throw ConfigError("JSON value is not object");
    return std::get<object_t>(value_);
}

JsonValue::array_t& JsonValue::as_array() {
    if (!is_array()) throw ConfigError("JSON value is not array");
    return std::get<array_t>(value_);
}

JsonValue::object_t& JsonValue::as_object() {
    if (!is_object()) throw ConfigError("JSON value is not object");
    return std::get<object_t>(value_);
}

bool JsonValue::truthy() const {
    if (is_null()) return false;
    if (is_bool()) return as_bool();
    return true;
}

const JsonValue* JsonValue::find(const std::string& key) const {
    if (!is_object()) return nullptr;
    const auto& obj = as_object();
    auto it = obj.find(key);
    return it == obj.end() ? nullptr : &it->second;
}

JsonValue* JsonValue::find(const std::string& key) {
    if (!is_object()) return nullptr;
    auto& obj = as_object();
    auto it = obj.find(key);
    return it == obj.end() ? nullptr : &it->second;
}

namespace {

class Parser {
public:
    explicit Parser(const std::string& text) : text_(text) {}

    JsonValue parse() {
        skip_ws();
        JsonValue value = parse_value();
        skip_ws();
        if (pos_ != text_.size()) throw ConfigError("failed to parse config JSON: trailing characters");
        return value;
    }

private:
    const std::string& text_;
    std::size_t pos_ = 0;

    void skip_ws() {
        while (pos_ < text_.size() && std::isspace(static_cast<unsigned char>(text_[pos_]))) ++pos_;
    }

    char peek() const {
        if (pos_ >= text_.size()) throw ConfigError("failed to parse config JSON: unexpected end of input");
        return text_[pos_];
    }

    bool consume(char ch) {
        if (pos_ < text_.size() && text_[pos_] == ch) {
            ++pos_;
            return true;
        }
        return false;
    }

    void expect(const std::string& token) {
        if (text_.compare(pos_, token.size(), token) != 0) {
            throw ConfigError("failed to parse config JSON: expected " + token);
        }
        pos_ += token.size();
    }

    JsonValue parse_value() {
        skip_ws();
        char ch = peek();
        if (ch == '{') return parse_object();
        if (ch == '[') return parse_array();
        if (ch == '"') return JsonValue(parse_string());
        if (ch == 't') {
            expect("true");
            return JsonValue(true);
        }
        if (ch == 'f') {
            expect("false");
            return JsonValue(false);
        }
        if (ch == 'n') {
            expect("null");
            return JsonValue(nullptr);
        }
        return JsonValue(parse_number());
    }

    JsonValue parse_object() {
        consume('{');
        JsonValue::object_t object;
        skip_ws();
        if (consume('}')) return JsonValue(object);
        while (true) {
            skip_ws();
            std::string key = parse_string();
            skip_ws();
            if (!consume(':')) throw ConfigError("failed to parse config JSON: expected ':'");
            skip_ws();
            object.emplace(std::move(key), parse_value());
            skip_ws();
            if (consume('}')) break;
            if (!consume(',')) throw ConfigError("failed to parse config JSON: expected ','");
        }
        return JsonValue(object);
    }

    JsonValue parse_array() {
        consume('[');
        JsonValue::array_t array;
        skip_ws();
        if (consume(']')) return JsonValue(array);
        while (true) {
            skip_ws();
            array.push_back(parse_value());
            skip_ws();
            if (consume(']')) break;
            if (!consume(',')) throw ConfigError("failed to parse config JSON: expected ','");
        }
        return JsonValue(array);
    }

    std::string parse_string() {
        if (!consume('"')) throw ConfigError("failed to parse config JSON: expected string");
        std::string out;
        while (pos_ < text_.size()) {
            char ch = text_[pos_++];
            if (ch == '"') return out;
            if (ch == '\\') {
                if (pos_ >= text_.size()) throw ConfigError("failed to parse config JSON: invalid escape");
                char esc = text_[pos_++];
                switch (esc) {
                    case '"': out.push_back('"'); break;
                    case '\\': out.push_back('\\'); break;
                    case '/': out.push_back('/'); break;
                    case 'b': out.push_back('\b'); break;
                    case 'f': out.push_back('\f'); break;
                    case 'n': out.push_back('\n'); break;
                    case 'r': out.push_back('\r'); break;
                    case 't': out.push_back('\t'); break;
                    case 'u': {
                        if (pos_ + 4 > text_.size()) throw ConfigError("failed to parse config JSON: incomplete unicode escape");
                        auto hex4 = [&]() -> uint32_t {
                            uint32_t cp = 0;
                            for (int k = 0; k < 4; ++k) {
                                char h = text_[pos_++];
                                cp <<= 4;
                                if (h >= '0' && h <= '9') cp |= static_cast<uint32_t>(h - '0');
                                else if (h >= 'a' && h <= 'f') cp |= static_cast<uint32_t>(h - 'a' + 10);
                                else if (h >= 'A' && h <= 'F') cp |= static_cast<uint32_t>(h - 'A' + 10);
                                else throw ConfigError("failed to parse config JSON: invalid unicode escape");
                            }
                            return cp;
                        };
                        uint32_t cp = hex4();
                        if (cp >= 0xD800 && cp <= 0xDBFF) {
                            if (pos_ + 6 > text_.size() || text_[pos_] != '\\' || text_[pos_ + 1] != 'u')
                                throw ConfigError("failed to parse config JSON: missing low surrogate");
                            pos_ += 2;
                            uint32_t lo = hex4();
                            if (lo < 0xDC00 || lo > 0xDFFF)
                                throw ConfigError("failed to parse config JSON: invalid low surrogate");
                            cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                        }
                        if (cp < 0x80) {
                            out.push_back(static_cast<char>(cp));
                        } else if (cp < 0x800) {
                            out.push_back(static_cast<char>(0xC0 | (cp >> 6)));
                            out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
                        } else if (cp < 0x10000) {
                            out.push_back(static_cast<char>(0xE0 | (cp >> 12)));
                            out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
                            out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
                        } else {
                            out.push_back(static_cast<char>(0xF0 | (cp >> 18)));
                            out.push_back(static_cast<char>(0x80 | ((cp >> 12) & 0x3F)));
                            out.push_back(static_cast<char>(0x80 | ((cp >> 6) & 0x3F)));
                            out.push_back(static_cast<char>(0x80 | (cp & 0x3F)));
                        }
                        break;
                    }
                    default: throw ConfigError("failed to parse config JSON: unsupported escape");
                }
            } else {
                out.push_back(ch);
            }
        }
        throw ConfigError("failed to parse config JSON: unterminated string");
    }

    double parse_number() {
        std::size_t start = pos_;
        if (text_[pos_] == '-') ++pos_;
        while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) ++pos_;
        if (pos_ < text_.size() && text_[pos_] == '.') {
            ++pos_;
            while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) ++pos_;
        }
        if (pos_ < text_.size() && (text_[pos_] == 'e' || text_[pos_] == 'E')) {
            ++pos_;
            if (pos_ < text_.size() && (text_[pos_] == '+' || text_[pos_] == '-')) ++pos_;
            while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) ++pos_;
        }
        double result = 0;
        auto [ptr, ec] = std::from_chars(text_.data() + start, text_.data() + pos_, result);
        if (ec != std::errc()) throw ConfigError("failed to parse config JSON: invalid number");
        return result;
    }
};

std::string indent(int depth, int spaces) { return std::string(depth * spaces, ' '); }

// go_format_json_number formats a double matching Go's encoding/json byte-for-byte.
// Go rule (encoding/json/encode.go floatEncoder): for float64, use 'f' format
// (fixed-point, shortest digits) when 1e-6 <= |x| < 1e21, else 'e' (scientific,
// shortest). Both with precision=-1 (shortest representation). Diverges from
// Go's fmt.Sprintf("%g") which uses different thresholds — keep them separate.
std::string go_format_json_number(double d) {
    if (d == 0.0) return std::signbit(d) ? "-0" : "0";
    double abs_d = std::abs(d);
    bool use_scientific = (abs_d < 1e-6) || (abs_d >= 1e21);
    char buf[64];
    auto fmt = use_scientific ? std::chars_format::scientific : std::chars_format::fixed;
    auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d, fmt);
    if (ec != std::errc()) {
        std::ostringstream oss;
        oss << std::setprecision(17) << d;
        return oss.str();
    }
    return std::string(buf, ptr);
}

// dump_impl writes the JSON serialization of `value` into `out`.
// The earlier signature returned a fresh std::string per call and used
// std::ostringstream for every array / object, allocating O(depth) extra
// buffers on deeply nested values. Threading the output `std::string&`
// through the recursion eliminates those temporaries — each character is
// appended exactly once into the caller's buffer.
void dump_impl(const JsonValue& value, int indent_size, int depth, std::string& out) {
    if (value.is_null()) { out += "null"; return; }
    if (value.is_bool()) { out += value.as_bool() ? "true" : "false"; return; }
    if (value.is_number()) { out += go_format_json_number(value.as_number()); return; }
    if (value.is_string()) {
        const auto& s = value.as_string();
        out.push_back('"');
        for (std::size_t i = 0; i < s.size(); ++i) {
            unsigned char ch = static_cast<unsigned char>(s[i]);
            switch (ch) {
                case '"': out += "\\\""; break;
                case '\\': out += "\\\\"; break;
                case '\n': out += "\\n"; break;
                case '\r': out += "\\r"; break;
                case '\t': out += "\\t"; break;
                case '<': out += "\\u003c"; break;
                case '>': out += "\\u003e"; break;
                case '&': out += "\\u0026"; break;
                default:
                    // U+2028 (E2 80 A8) and U+2029 (E2 80 A9): escape like Go's encoding/json
                    if (ch == 0xE2 && i + 2 < s.size()
                        && static_cast<unsigned char>(s[i+1]) == 0x80
                        && (static_cast<unsigned char>(s[i+2]) == 0xA8 || static_cast<unsigned char>(s[i+2]) == 0xA9)) {
                        out += (static_cast<unsigned char>(s[i+2]) == 0xA8) ? "\\u2028" : "\\u2029";
                        i += 2;
                    } else {
                        out.push_back(static_cast<char>(ch));
                    }
                    break;
            }
        }
        out.push_back('"');
        return;
    }
    if (value.is_array()) {
        const auto& array = value.as_array();
        if (array.empty()) { out += "[]"; return; }
        // R13-1: compact mode (indent_size == 0) suppresses all '\n' and
        // indent strings — matches Go json.Marshal output used by
        // observe_log / [pine-debug] log lines and HTTP /execute trace
        // payloads. Indented mode kept identical to the original layout.
        const bool compact = (indent_size == 0);
        out.push_back('[');
        if (!compact) out.push_back('\n');
        const std::string inner_indent = compact ? std::string()
            : std::string(static_cast<std::size_t>((depth + 1) * indent_size), ' ');
        const std::string outer_indent = compact ? std::string()
            : std::string(static_cast<std::size_t>(depth * indent_size), ' ');
        for (std::size_t i = 0; i < array.size(); ++i) {
            out += inner_indent;
            dump_impl(array[i], indent_size, depth + 1, out);
            if (i + 1 != array.size()) {
                out.push_back(',');
                if (compact) {
                    // no trailing space — Go json.Marshal uses bare ','
                } else {
                    out.push_back('\n');
                }
            } else if (!compact) {
                out.push_back('\n');
            }
        }
        out += outer_indent;
        out.push_back(']');
        return;
    }
    const auto& object = value.as_object();
    if (object.empty()) { out += "{}"; return; }
    const bool compact = (indent_size == 0);
    out.push_back('{');
    if (!compact) out.push_back('\n');
    const std::string inner_indent = compact ? std::string()
        : std::string(static_cast<std::size_t>((depth + 1) * indent_size), ' ');
    const std::string outer_indent = compact ? std::string()
        : std::string(static_cast<std::size_t>(depth * indent_size), ' ');
    std::size_t index = 0;
    for (const auto& [key, item] : object) {
        out += inner_indent;
        out.push_back('"');
        out += key;
        // Go json.Marshal compact form: `"k":v` (no space). Indent form
        // keeps `"k": v` for human readability.
        out += compact ? "\":" : "\": ";
        dump_impl(item, indent_size, depth + 1, out);
        if (++index != object.size()) {
            out.push_back(',');
            if (!compact) out.push_back('\n');
        } else if (!compact) {
            out.push_back('\n');
        }
    }
    out += outer_indent;
    out.push_back('}');
}

}  // namespace

JsonValue parse_json(const std::string& text) { return Parser(text).parse(); }
std::string dump_json(const JsonValue& value, int indent) {
    std::string out;
    out.reserve(64);
    dump_impl(value, indent, 0, out);
    // R13-1: compact mode suppresses the trailing newline too so a single
    // dump_json(value, 0) result fits in one line (observe_log / pine-debug
    // log line filters depend on this). Indented mode keeps the trailing
    // '\n' for human-readable file output.
    if (indent != 0) out.push_back('\n');
    return out;
}

}  // namespace pine
