#include "pine/pine.hpp"

#include <cctype>
#include <charconv>
#include <cmath>
#include <cstdint>
#include <fstream>
#include <iomanip>
#include <sstream>

#include "config/json_writer.hpp"

namespace pine {

Variant::Variant() : value_(nullptr) {
}
Variant::Variant(std::nullptr_t) : value_(nullptr) {
}
Variant::Variant(bool value) : value_(value) {
}
Variant::Variant(double value) : value_(value) {
}
Variant::Variant(int value) : value_(static_cast<double>(value)) {
}
Variant::Variant(std::string value) : value_(std::move(value)) {
}
Variant::Variant(const char* value) : value_(std::string(value)) {
}
Variant::Variant(array_t value) : value_(std::move(value)) {
}
Variant::Variant(object_t value) : value_(std::move(value)) {
}

bool Variant::is_null() const {
  return std::holds_alternative<std::nullptr_t>(value_);
}
bool Variant::is_bool() const {
  return std::holds_alternative<bool>(value_);
}
bool Variant::is_number() const {
  return std::holds_alternative<double>(value_);
}
bool Variant::is_string() const {
  return std::holds_alternative<std::string>(value_);
}
bool Variant::is_array() const {
  return std::holds_alternative<array_t>(value_);
}
bool Variant::is_object() const {
  return std::holds_alternative<object_t>(value_);
}

bool Variant::as_bool() const {
  if (!is_bool()) {
    throw ConfigError("JSON value is not bool");
  }
  return std::get<bool>(value_);
}

double Variant::as_number() const {
  if (!is_number()) {
    throw ConfigError("JSON value is not number");
  }
  return std::get<double>(value_);
}

const std::string& Variant::as_string() const {
  if (!is_string()) {
    throw ConfigError("JSON value is not string");
  }
  return std::get<std::string>(value_);
}

const Variant::array_t& Variant::as_array() const {
  if (!is_array()) {
    throw ConfigError("JSON value is not array");
  }
  return std::get<array_t>(value_);
}

const Variant::object_t& Variant::as_object() const {
  if (!is_object()) {
    throw ConfigError("JSON value is not object");
  }
  return std::get<object_t>(value_);
}

Variant::array_t& Variant::as_array() {
  if (!is_array()) {
    throw ConfigError("JSON value is not array");
  }
  return std::get<array_t>(value_);
}

Variant::object_t& Variant::as_object() {
  if (!is_object()) {
    throw ConfigError("JSON value is not object");
  }
  return std::get<object_t>(value_);
}

bool Variant::truthy() const {
  if (is_null()) {
    return false;
  }
  if (is_bool()) {
    return as_bool();
  }
  return true;
}

const Variant* Variant::find(const std::string& key) const {
  if (!is_object()) {
    return nullptr;
  }
  const auto& obj = as_object();
  auto it = obj.find(key);
  return it == obj.end() ? nullptr : &it->second;
}

Variant* Variant::find(const std::string& key) {
  if (!is_object()) {
    return nullptr;
  }
  auto& obj = as_object();
  auto it = obj.find(key);
  return it == obj.end() ? nullptr : &it->second;
}

namespace {

class Parser {
 public:
  explicit Parser(const std::string& text) : text_(text) {
  }

  Variant parse() {
    skip_ws();
    Variant value = parse_value();
    skip_ws();
    if (pos_ != text_.size()) {
      throw ConfigError("failed to parse config JSON: trailing characters");
    }
    return value;
  }

 private:
  const std::string& text_;
  std::size_t pos_ = 0;

  void skip_ws() {
    while (pos_ < text_.size() && std::isspace(static_cast<unsigned char>(text_[pos_]))) {
      ++pos_;
    }
  }

  char peek() const {
    if (pos_ >= text_.size()) {
      throw ConfigError("failed to parse config JSON: unexpected end of input");
    }
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

  Variant parse_value() {
    skip_ws();
    char ch = peek();
    if (ch == '{') {
      return parse_object();
    }
    if (ch == '[') {
      return parse_array();
    }
    if (ch == '"') {
      return Variant(parse_string());
    }
    if (ch == 't') {
      expect("true");
      return Variant(true);
    }
    if (ch == 'f') {
      expect("false");
      return Variant(false);
    }
    if (ch == 'n') {
      expect("null");
      return Variant(nullptr);
    }
    return Variant(parse_number());
  }

  Variant parse_object() {
    consume('{');
    Variant::object_t object;
    skip_ws();
    if (consume('}')) {
      return Variant(object);
    }
    while (true) {
      skip_ws();
      std::string key = parse_string();
      skip_ws();
      if (!consume(':')) {
        throw ConfigError("failed to parse config JSON: expected ':'");
      }
      skip_ws();
      object.emplace(std::move(key), parse_value());
      skip_ws();
      if (consume('}')) {
        break;
      }
      if (!consume(',')) {
        throw ConfigError("failed to parse config JSON: expected ','");
      }
    }
    return Variant(object);
  }

  Variant parse_array() {
    consume('[');
    Variant::array_t array;
    skip_ws();
    if (consume(']')) {
      return Variant(array);
    }
    while (true) {
      skip_ws();
      array.push_back(parse_value());
      skip_ws();
      if (consume(']')) {
        break;
      }
      if (!consume(',')) {
        throw ConfigError("failed to parse config JSON: expected ','");
      }
    }
    return Variant(array);
  }

  std::string parse_string() {
    if (!consume('"')) {
      throw ConfigError("failed to parse config JSON: expected string");
    }
    std::string out;
    while (pos_ < text_.size()) {
      char ch = text_[pos_++];
      if (ch == '"') {
        return out;
      }
      if (ch == '\\') {
        if (pos_ >= text_.size()) {
          throw ConfigError("failed to parse config JSON: invalid escape");
        }
        char esc = text_[pos_++];
        switch (esc) {
          case '"':
            out.push_back('"');
            break;
          case '\\':
            out.push_back('\\');
            break;
          case '/':
            out.push_back('/');
            break;
          case 'b':
            out.push_back('\b');
            break;
          case 'f':
            out.push_back('\f');
            break;
          case 'n':
            out.push_back('\n');
            break;
          case 'r':
            out.push_back('\r');
            break;
          case 't':
            out.push_back('\t');
            break;
          case 'u': {
            if (pos_ + 4 > text_.size()) {
              throw ConfigError("failed to parse config JSON: incomplete unicode escape");
            }
            auto hex4 = [&]() -> uint32_t {
              uint32_t cp = 0;
              for (int k = 0; k < 4; ++k) {
                char h = text_[pos_++];
                cp <<= 4;
                if (h >= '0' && h <= '9') {
                  cp |= static_cast<uint32_t>(h - '0');
                } else if (h >= 'a' && h <= 'f') {
                  cp |= static_cast<uint32_t>(h - 'a' + 10);
                } else if (h >= 'A' && h <= 'F') {
                  cp |= static_cast<uint32_t>(h - 'A' + 10);
                } else {
                  throw ConfigError("failed to parse config JSON: invalid unicode escape");
                }
              }
              return cp;
            };
            uint32_t cp = hex4();
            if (cp >= 0xD800 && cp <= 0xDBFF) {
              if (pos_ + 6 > text_.size() || text_[pos_] != '\\' || text_[pos_ + 1] != 'u') {
                throw ConfigError("failed to parse config JSON: missing low surrogate");
              }
              pos_ += 2;
              uint32_t lo = hex4();
              if (lo < 0xDC00 || lo > 0xDFFF) {
                throw ConfigError("failed to parse config JSON: invalid low surrogate");
              }
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
          default:
            throw ConfigError("failed to parse config JSON: unsupported escape");
        }
      } else {
        out.push_back(ch);
      }
    }
    throw ConfigError("failed to parse config JSON: unterminated string");
  }

  double parse_number() {
    std::size_t start = pos_;
    if (text_[pos_] == '-') {
      ++pos_;
    }
    while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) {
      ++pos_;
    }
    if (pos_ < text_.size() && text_[pos_] == '.') {
      ++pos_;
      while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) {
        ++pos_;
      }
    }
    if (pos_ < text_.size() && (text_[pos_] == 'e' || text_[pos_] == 'E')) {
      ++pos_;
      if (pos_ < text_.size() && (text_[pos_] == '+' || text_[pos_] == '-')) {
        ++pos_;
      }
      while (pos_ < text_.size() && std::isdigit(static_cast<unsigned char>(text_[pos_]))) {
        ++pos_;
      }
    }
    double result = 0;
    auto [ptr, ec] = std::from_chars(text_.data() + start, text_.data() + pos_, result);
    if (ec != std::errc()) {
      throw ConfigError("failed to parse config JSON: invalid number");
    }
    return result;
  }
};

}  // namespace

namespace {

// Splits a shortest-round-trip rendering from std::to_chars into a sign, a
// digit string with no decimal point, and a decimal exponent such that the
// value is sign * 0.<digits> * 10^exp10. to_chars picks fixed or scientific on
// its own, so both shapes have to be accepted here.
struct DecimalParts {
  bool negative = false;
  std::string digits;  // significant digits, no '.', no leading zeros
  int exp10 = 0;       // number of digits before the decimal point
};

DecimalParts go_json_decompose(const std::string& s) {
  DecimalParts p;
  std::size_t i = 0;
  if (i < s.size() && (s[i] == '-' || s[i] == '+')) {
    p.negative = (s[i] == '-');
    ++i;
  }
  std::string mantissa;
  int point_pos = -1;
  for (; i < s.size(); ++i) {
    if (s[i] == '.') {
      point_pos = static_cast<int>(mantissa.size());
      continue;
    }
    if (s[i] == 'e' || s[i] == 'E') {
      ++i;
      int sign = 1;
      if (i < s.size() && (s[i] == '-' || s[i] == '+')) {
        sign = (s[i] == '-') ? -1 : 1;
        ++i;
      }
      int e = 0;
      for (; i < s.size(); ++i) {
        e = e * 10 + (s[i] - '0');
      }
      p.exp10 = sign * e;
      break;
    }
    mantissa.push_back(s[i]);
  }
  if (point_pos < 0) {
    point_pos = static_cast<int>(mantissa.size());
  }
  // exp10 so far holds only the explicit exponent; add the point position.
  p.exp10 += point_pos;
  // Strip leading zeros, adjusting the exponent as we go (0.001 -> digits "1").
  std::size_t lead = 0;
  while (lead < mantissa.size() && mantissa[lead] == '0') {
    ++lead;
    --p.exp10;
  }
  mantissa.erase(0, lead);
  // Strip trailing zeros: they carry no information in this representation.
  while (!mantissa.empty() && mantissa.back() == '0') {
    mantissa.pop_back();
  }
  p.digits = mantissa;
  return p;
}

// Renders as Go's strconv.FormatFloat(d, 'f', -1, 64) does: plain decimal, no
// exponent, shortest digits zero-filled out to the decimal point.
std::string go_json_to_fixed(const std::string& shortest) {
  DecimalParts p = go_json_decompose(shortest);
  if (p.digits.empty()) {
    return p.negative ? "-0" : "0";
  }
  std::string out;
  if (p.negative) {
    out.push_back('-');
  }
  int nd = static_cast<int>(p.digits.size());
  if (p.exp10 <= 0) {
    out += "0.";
    out.append(static_cast<std::size_t>(-p.exp10), '0');
    out += p.digits;
  } else if (p.exp10 >= nd) {
    out += p.digits;
    out.append(static_cast<std::size_t>(p.exp10 - nd), '0');
  } else {
    out.append(p.digits, 0, static_cast<std::size_t>(p.exp10));
    out.push_back('.');
    out.append(p.digits, static_cast<std::size_t>(p.exp10), std::string::npos);
  }
  return out;
}

// Renders as Go's encoding/json does for the scientific branch: strconv's
// 'e' format, then ONE leading zero trimmed from a two-digit negative
// exponent. Go's json does exactly that and nothing more, so 1e-7 prints as
// "1e-7" while 1e+21 keeps its "+21" and 1e-100 keeps all three digits
// (verified against encoding/json, not inferred).
std::string go_json_to_scientific(const std::string& shortest) {
  DecimalParts p = go_json_decompose(shortest);
  if (p.digits.empty()) {
    return p.negative ? "-0" : "0";
  }
  std::string out;
  if (p.negative) {
    out.push_back('-');
  }
  out.push_back(p.digits[0]);
  if (p.digits.size() > 1) {
    out.push_back('.');
    out.append(p.digits, 1, std::string::npos);
  }
  int e = p.exp10 - 1;  // 0.<digits> * 10^exp10 == <d0>.<rest> * 10^(exp10-1)
  out.push_back('e');
  if (e < 0) {
    out.push_back('-');
    e = -e;
  } else {
    out.push_back('+');
  }
  std::string es = std::to_string(e);
  if (es.size() < 2) {
    es.insert(es.begin(), '0');  // strconv pads to at least two digits
  }
  if (p.exp10 - 1 < 0 && es.size() == 2 && es[0] == '0') {
    es.erase(es.begin());  // json trims that pad back off for negatives only
  }
  out += es;
  return out;
}

}  // namespace

// go_format_json_number formats a double matching Go's encoding/json byte-for-byte.
// Go rule (encoding/json/encode.go floatEncoder): for float64, use 'f' format
// (fixed-point, shortest digits) when 1e-6 <= |x| < 1e21, else 'e' (scientific,
// shortest). Both with precision=-1 (shortest representation). Diverges from
// Go's fmt.Sprintf("%g") which uses different thresholds — keep them separate.
std::string go_format_json_number(double d) {
  // Non-finite first, before to_chars. There is no Go byte sequence to match:
  // encoding/json errors out on NaN/Inf, so no choice here is "correct" and the
  // aim is only to avoid making things worse. These are the exact strings the
  // pre-#180 implementation produced (raw to_chars output), kept verbatim so
  // this change does not alter behaviour beyond fixing the corruption below.
  // Callers are expected to reject non-finite before serializing — engine.cpp
  // validates on the write path — so reaching here means that guard was bypassed.
  //
  // The check must be here rather than relying on the `ec` fallback further
  // down: to_chars SUCCEEDS on these inputs and writes "inf" / "-inf" / "nan",
  // so the error path never fires. Those letters then reached
  // go_json_decompose, which read 'i' as a mantissa digit and 'f' as an
  // exponent digit and emitted "i.nfe+02" — still invalid JSON, but now
  // corrupted rather than merely non-standard.
  if (std::isnan(d)) {
    return "nan";
  }
  if (std::isinf(d)) {
    return d < 0 ? "-inf" : "inf";
  }
  if (d == 0.0) {
    return std::signbit(d) ? "-0" : "0";
  }
  double abs_d = std::abs(d);
  bool use_scientific = (abs_d < 1e-6) || (abs_d >= 1e21);
  char buf[64];

  // Always source the digits from chars_format::scientific, then reposition the
  // decimal point ourselves. Go uses strconv.FormatFloat(d, 'f'|'e', -1, 64),
  // where precision -1 means "fewest digits that round-trip".
  //
  // Neither of the other to_chars modes gives that. chars_format::fixed prints
  // the value exactly, and so does the default overload once the magnitude is
  // large enough to render without an exponent — both turn
  // 1.0000000000000002e20 into 100000000000000016384 where Go emits
  // 100000000000000020000. Same double, different bytes: that is issue #180.
  // Only the scientific form is guaranteed shortest-round-trip, so it is the
  // single source of digits for both output shapes.
  auto [ptr, ec] = std::to_chars(buf, buf + sizeof(buf), d, std::chars_format::scientific);
  if (ec != std::errc()) {
    std::ostringstream oss;
    oss << std::setprecision(17) << d;
    return oss.str();
  }
  std::string shortest(buf, ptr);
  return use_scientific ? go_json_to_scientific(shortest) : go_json_to_fixed(shortest);
}

namespace {
// The earlier signature returned a fresh std::string per call and used
// std::ostringstream for every array / object, allocating O(depth) extra
// buffers on deeply nested values. Threading the output `std::string&`
// through the recursion eliminates those temporaries — each character is
// appended exactly once into the caller's buffer.
void dump_impl(const Variant& value, int indent_size, int depth, std::string& out) {
  if (value.is_null()) {
    out += "null";
    return;
  }
  if (value.is_bool()) {
    out += value.as_bool() ? "true" : "false";
    return;
  }
  if (value.is_number()) {
    out += go_format_json_number(value.as_number());
    return;
  }
  if (value.is_string()) {
    const auto& s = value.as_string();
    out.push_back('"');
    for (std::size_t i = 0; i < s.size(); ++i) {
      unsigned char ch = static_cast<unsigned char>(s[i]);
      switch (ch) {
        case '"':
          out += "\\\"";
          break;
        case '\\':
          out += "\\\\";
          break;
        case '\n':
          out += "\\n";
          break;
        case '\r':
          out += "\\r";
          break;
        case '\t':
          out += "\\t";
          break;
        case '<':
          out += "\\u003c";
          break;
        case '>':
          out += "\\u003e";
          break;
        case '&':
          out += "\\u0026";
          break;
        default:
          // U+2028 (E2 80 A8) and U+2029 (E2 80 A9): escape like Go's encoding/json
          if (ch == 0xE2 && i + 2 < s.size() && static_cast<unsigned char>(s[i + 1]) == 0x80 &&
              (static_cast<unsigned char>(s[i + 2]) == 0xA8 ||
               static_cast<unsigned char>(s[i + 2]) == 0xA9)) {
            out += (static_cast<unsigned char>(s[i + 2]) == 0xA8) ? "\\u2028" : "\\u2029";
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
    if (array.empty()) {
      out += "[]";
      return;
    }
    // R13-1: compact mode (indent_size == 0) suppresses all '\n' and
    // indent strings — matches Go json.Marshal output used by
    // observe_log / [pine-debug] log lines and HTTP /execute trace
    // payloads. Indented mode kept identical to the original layout.
    const bool compact = (indent_size == 0);
    out.push_back('[');
    if (!compact) {
      out.push_back('\n');
    }
    const std::string inner_indent =
        compact ? std::string() : std::string(static_cast<std::size_t>((depth + 1) * indent_size), ' ');
    const std::string outer_indent =
        compact ? std::string() : std::string(static_cast<std::size_t>(depth * indent_size), ' ');
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
  if (object.empty()) {
    out += "{}";
    return;
  }
  const bool compact = (indent_size == 0);
  out.push_back('{');
  if (!compact) {
    out.push_back('\n');
  }
  const std::string inner_indent =
      compact ? std::string() : std::string(static_cast<std::size_t>((depth + 1) * indent_size), ' ');
  const std::string outer_indent =
      compact ? std::string() : std::string(static_cast<std::size_t>(depth * indent_size), ' ');
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
      if (!compact) {
        out.push_back('\n');
      }
    } else if (!compact) {
      out.push_back('\n');
    }
  }
  out += outer_indent;
  out.push_back('}');
}

}  // namespace

Variant parse_json(const std::string& text) {
  return Parser(text).parse();
}
std::string dump_json(const Variant& value, int indent) {
  return dump_json_fast(value, indent);
}

}  // namespace pine
