#include "config/json_writer.hpp"

#include <rapidjson/prettywriter.h>
#include <rapidjson/stringbuffer.h>
#include <rapidjson/writer.h>

namespace pine {

std::string dump_json_fast(const JsonValue& value, int indent) {
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

std::string result_common_to_json(const std::map<std::string, JsonValue>& common) {
  rapidjson::StringBuffer sb;
  sb.Reserve(512);
  rapidjson::Writer<rapidjson::StringBuffer> w(sb);
  w.StartObject();
  for (const auto& [k, v] : common) {
    w.Key(k.c_str(), static_cast<rapidjson::SizeType>(k.size()));
    write_json_value(w, v);
  }
  w.EndObject();
  return std::string(sb.GetString(), sb.GetSize());
}

std::string result_items_to_json(const std::vector<std::map<std::string, JsonValue>>& items) {
  rapidjson::StringBuffer sb;
  sb.Reserve(items.size() * 128 + 2);
  rapidjson::Writer<rapidjson::StringBuffer> w(sb);
  w.StartArray();
  for (const auto& row : items) {
    w.StartObject();
    for (const auto& [k, v] : row) {
      w.Key(k.c_str(), static_cast<rapidjson::SizeType>(k.size()));
      write_json_value(w, v);
    }
    w.EndObject();
  }
  w.EndArray();
  return std::string(sb.GetString(), sb.GetSize());
}

}  // namespace pine
