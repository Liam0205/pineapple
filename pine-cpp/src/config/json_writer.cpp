#include "config/json_writer.hpp"

#include <rapidjson/prettywriter.h>
#include <rapidjson/stringbuffer.h>
#include <rapidjson/writer.h>

namespace pine {

std::string dump_json_fast(const Variant& value, int indent) {
  rapidjson::StringBuffer sb;
  sb.Reserve(4096);
  if (indent == 0) {
    rapidjson::Writer<rapidjson::StringBuffer> w(sb);
    write_json_value(w, value);
  } else {
    rapidjson::PrettyWriter<rapidjson::StringBuffer> w(sb);
    w.SetIndent(' ', static_cast<unsigned>(indent));
    write_json_value(w, value);
    sb.Put('\n');
  }
  return std::string(sb.GetString(), sb.GetSize());
}

std::string result_common_to_json(const Variant::object_t& common) {
  rapidjson::StringBuffer sb;
  sb.Reserve(512);
  rapidjson::Writer<rapidjson::StringBuffer> w(sb);
  std::vector<const std::string*> keys;
  keys.reserve(common.size());
  for (const auto& [k, _] : common) {
    keys.push_back(&k);
  }
  std::sort(keys.begin(), keys.end(),
            [](const std::string* a, const std::string* b) { return *a < *b; });
  w.StartObject();
  for (const auto* key : keys) {
    w.Key(key->c_str(), static_cast<rapidjson::SizeType>(key->size()));
    write_json_value(w, common.find(*key)->second);
  }
  w.EndObject();
  return std::string(sb.GetString(), sb.GetSize());
}

std::string result_items_to_json(const std::vector<Variant::object_t>& items) {
  rapidjson::StringBuffer sb;
  sb.Reserve(items.size() * 128 + 2);
  rapidjson::Writer<rapidjson::StringBuffer> w(sb);
  w.StartArray();
  for (const auto& row : items) {
    std::vector<const std::string*> keys;
    keys.reserve(row.size());
    for (const auto& [k, _] : row) {
      keys.push_back(&k);
    }
    std::sort(keys.begin(), keys.end(),
              [](const std::string* a, const std::string* b) { return *a < *b; });
    w.StartObject();
    for (const auto* key : keys) {
      w.Key(key->c_str(), static_cast<rapidjson::SizeType>(key->size()));
      write_json_value(w, row.find(*key)->second);
    }
    w.EndObject();
  }
  w.EndArray();
  return std::string(sb.GetString(), sb.GetSize());
}

}  // namespace pine
