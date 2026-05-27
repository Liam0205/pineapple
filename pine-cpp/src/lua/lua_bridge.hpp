#pragma once

#include "pine/pine.hpp"

#include <map>
#include <set>
#include <string>

struct lua_State;

namespace pine {
namespace lua {

class LuaSnapshot {
 public:
  LuaSnapshot() = default;
  explicit LuaSnapshot(lua_State* L);

  std::map<std::string, int> capture_values(lua_State* L) const;
  void reset_to_baseline(lua_State* L, const std::map<std::string, int>& borrow_snap) const;

 private:
  std::set<std::string> globals;
};

class LuaVM {
 public:
  LuaVM();
  ~LuaVM();

  LuaVM(const LuaVM&) = delete;
  LuaVM& operator=(const LuaVM&) = delete;

  void load_script(const std::string& code, const std::string& op_name);
  void set_global(const std::string& name, const JsonValue& value);
  void set_global_table(const std::string& name, const std::vector<JsonValue>& values);
  std::vector<JsonValue> call_function(const std::string& func_name, int nret, const std::string& op_name);

  lua_State* state() const {
    return L_;
  }

 private:
  void push_value(const JsonValue& value);
  JsonValue to_value(int index);
  lua_State* L_;
};

}  // namespace lua
}  // namespace pine
