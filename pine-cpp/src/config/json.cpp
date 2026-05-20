#include "pine/pine.hpp"

#include <cctype>
#include <charconv>
#include <cmath>
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
        return std::stod(text_.substr(start, pos_ - start));
    }
};

std::string indent(int depth, int spaces) { return std::string(depth * spaces, ' '); }

std::string dump_impl(const JsonValue& value, int indent_size, int depth) {
    if (value.is_null()) return "null";
    if (value.is_bool()) return value.as_bool() ? "true" : "false";
    if (value.is_number()) {
        double number = value.as_number();
        if (number == 0.0) number = 0.0;
        if (std::floor(number) == number && !std::isinf(number)) {
            std::ostringstream oss;
            oss << std::fixed << std::setprecision(1) << number;
            return oss.str();
        }
        char buf[32];
        auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), number);
        if (ec == std::errc()) return std::string(buf, ptr);
        std::ostringstream oss;
        oss << std::setprecision(17) << number;
        return oss.str();
    }
    if (value.is_string()) {
        std::string escaped;
        for (char ch : value.as_string()) {
            switch (ch) {
                case '"': escaped += "\\\""; break;
                case '\\': escaped += "\\\\"; break;
                case '\n': escaped += "\\n"; break;
                case '\r': escaped += "\\r"; break;
                case '\t': escaped += "\\t"; break;
                default: escaped.push_back(ch); break;
            }
        }
        return "\"" + escaped + "\"";
    }
    if (value.is_array()) {
        const auto& array = value.as_array();
        if (array.empty()) return "[]";
        std::ostringstream oss;
        oss << "[\n";
        for (std::size_t i = 0; i < array.size(); ++i) {
            oss << indent(depth + 1, indent_size) << dump_impl(array[i], indent_size, depth + 1);
            if (i + 1 != array.size()) oss << ',';
            oss << '\n';
        }
        oss << indent(depth, indent_size) << ']';
        return oss.str();
    }
    const auto& object = value.as_object();
    if (object.empty()) return "{}";
    std::ostringstream oss;
    oss << "{\n";
    std::size_t index = 0;
    for (const auto& [key, item] : object) {
        oss << indent(depth + 1, indent_size) << '"' << key << "\": " << dump_impl(item, indent_size, depth + 1);
        if (++index != object.size()) oss << ',';
        oss << '\n';
    }
    oss << indent(depth, indent_size) << '}';
    return oss.str();
}

}  // namespace

JsonValue parse_json(const std::string& text) { return Parser(text).parse(); }
std::string dump_json(const JsonValue& value, int indent) { return dump_impl(value, indent, 0) + "\n"; }

}  // namespace pine
