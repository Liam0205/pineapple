#pragma once

// RapidJSON-based JSON serialization for pine::Variant.
//
// Produces output byte-for-byte compatible with Go's encoding/json:
//   - Numbers: go_format_json_number (Grisu2-style shortest representation)
//   - Strings: escapes ", \, control chars, <, >, &, U+2028, U+2029
//   - Objects: keys in iteration order (caller controls ordering)
//
// Usage:
//   rapidjson::StringBuffer sb;
//   rapidjson::Writer<rapidjson::StringBuffer> w(sb);
//   pine::write_json_value(w, value);
//   std::string result(sb.GetString(), sb.GetSize());

#include "pine/pine.hpp"

#include <rapidjson/stringbuffer.h>
#include <rapidjson/writer.h>

#include <algorithm>
#include <cstdint>
#include <cstring>
#include <vector>

namespace pine {

// go_format_json_number is defined in json.cpp; declared here for use by
// the writer without pulling in the full translation unit.
std::string go_format_json_number(double d);

namespace detail {

// Write a Go-compatible escaped JSON string (with surrounding quotes)
// directly into a RapidJSON StringBuffer. Uses bulk append for safe runs.
inline void write_go_string(rapidjson::StringBuffer& sb, const std::string& s) {
  sb.Put('"');
  const char* p = s.data();
  const char* end = p + s.size();
  const char* safe = p;

  while (p < end) {
    unsigned char ch = static_cast<unsigned char>(*p);
    const char* esc = nullptr;
    std::size_t esc_len = 0;
    std::size_t skip = 1;

    if (ch == '"')       { esc = "\\\"";   esc_len = 2; }
    else if (ch == '\\') { esc = "\\\\";   esc_len = 2; }
    else if (ch == '\n') { esc = "\\n";    esc_len = 2; }
    else if (ch == '\r') { esc = "\\r";    esc_len = 2; }
    else if (ch == '\t') { esc = "\\t";    esc_len = 2; }
    else if (ch == '<')  { esc = "\\u003c"; esc_len = 6; }
    else if (ch == '>')  { esc = "\\u003e"; esc_len = 6; }
    else if (ch == '&')  { esc = "\\u0026"; esc_len = 6; }
    else if (ch < 0x20) {
      // flush safe prefix
      if (p > safe) {
        sb.Reserve(static_cast<rapidjson::SizeType>(p - safe));
        for (const char* q = safe; q < p; ++q) sb.PutUnsafe(*q);
      }
      char buf[8];
      int n = std::snprintf(buf, sizeof(buf), "\\u%04x", ch);
      for (int i = 0; i < n; ++i) sb.Put(buf[i]);
      safe = p + 1;
      ++p;
      continue;
    } else if (ch == 0xE2 && p + 2 < end) {
      unsigned char b1 = static_cast<unsigned char>(p[1]);
      unsigned char b2 = static_cast<unsigned char>(p[2]);
      if (b1 == 0x80 && (b2 == 0xA8 || b2 == 0xA9)) {
        esc = (b2 == 0xA8) ? "\\u2028" : "\\u2029";
        esc_len = 6;
        skip = 3;
      }
    }

    if (esc) {
      // flush safe prefix
      if (p > safe) {
        sb.Reserve(static_cast<rapidjson::SizeType>(p - safe));
        for (const char* q = safe; q < p; ++q) sb.PutUnsafe(*q);
      }
      for (std::size_t i = 0; i < esc_len; ++i) sb.Put(esc[i]);
      p += skip;
      safe = p;
    } else {
      ++p;
    }
  }

  // flush remaining safe suffix
  if (p > safe) {
    sb.Reserve(static_cast<rapidjson::SizeType>(p - safe));
    for (const char* q = safe; q < p; ++q) sb.PutUnsafe(*q);
  }
  sb.Put('"');
}

}  // namespace detail

// Write a Variant into a RapidJSON Writer.
// Objects keys are sorted lexicographically to match Go's encoding/json output.
template <typename Writer>
void write_json_value(Writer& w, const Variant& v) {
  if (v.is_null()) {
    w.Null();
    return;
  }
  if (v.is_bool()) {
    w.Bool(v.as_bool());
    return;
  }
  if (v.is_number()) {
    std::string num = go_format_json_number(v.as_number());
    w.RawValue(num.c_str(), num.size(), rapidjson::kNumberType);
    return;
  }
  if (v.is_string()) {
    // Reuse a thread-local buffer to avoid per-string heap allocation.
    // Clear() resets position without freeing memory, so the buffer grows
    // once per thread and stays allocated for the thread's lifetime.
    thread_local rapidjson::StringBuffer tmp;
    tmp.Clear();
    detail::write_go_string(tmp, v.as_string());
    w.RawValue(tmp.GetString(), tmp.GetSize(), rapidjson::kStringType);
    return;
  }
  if (v.is_array()) {
    w.StartArray();
    for (const auto& item : v.as_array()) {
      write_json_value(w, item);
    }
    w.EndArray();
    return;
  }
  // object — sort keys for deterministic output (matches Go encoding/json)
  const auto& obj = v.as_object();
  std::vector<const std::string*> keys;
  keys.reserve(obj.size());
  for (const auto& [k, _] : obj) {
    keys.push_back(&k);
  }
  std::sort(keys.begin(), keys.end(),
            [](const std::string* a, const std::string* b) { return *a < *b; });
  w.StartObject();
  for (const auto* key : keys) {
    w.Key(key->c_str(), static_cast<rapidjson::SizeType>(key->size()));
    write_json_value(w, obj.find(*key)->second);
  }
  w.EndObject();
}

// Serialize a Variant to a std::string using RapidJSON.
// indent=0 → compact (matches Go json.Marshal); indent>0 → pretty-printed.
std::string dump_json_fast(const Variant& value, int indent = 0);

// Serialize a Result's common map to compact JSON.
std::string result_common_to_json(const Variant::object_t& common);

// Serialize a Result's items array to compact JSON.
std::string result_items_to_json(const std::vector<Variant::object_t>& items);

}  // namespace pine
