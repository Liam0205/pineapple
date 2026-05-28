#define LUA_COMPAT_ALL 1

#include "lua_bridge.hpp"

extern "C" {
#include <lauxlib.h>
#include <lua.h>
#include <lualib.h>
}

#include <algorithm>
#include <cmath>
#include <set>
#include <string_view>
#include <vector>

// LuaJIT 2.1 compat
#ifndef lua_pushglobaltable
#define lua_pushglobaltable(L) lua_pushvalue(L, LUA_GLOBALSINDEX)
#endif

namespace pine {
namespace lua {

LuaSnapshot::LuaSnapshot(lua_State* L) {
  lua_pushglobaltable(L);
  lua_pushnil(L);
  while (lua_next(L, -2) != 0) {
    if (lua_type(L, -2) == LUA_TSTRING) {
      std::string key = lua_tostring(L, -2);
      globals.insert(key);
    }
    lua_pop(L, 1);
  }
  lua_pop(L, 1);
}

std::map<std::string, int> LuaSnapshot::capture_values(lua_State* L) const {
  std::map<std::string, int> values;
  // Pushing references to the registry to keep them alive
  for (const auto& key : globals) {
    lua_getglobal(L, key.c_str());
    int ref = luaL_ref(L, LUA_REGISTRYINDEX);
    values[key] = ref;
  }
  return values;
}

void LuaSnapshot::reset_to_baseline(lua_State* L, const std::map<std::string, int>& borrow_snap) const {
  lua_pushglobaltable(L);
  lua_pushnil(L);
  std::vector<std::string> to_remove;
  while (lua_next(L, -2) != 0) {
    if (lua_type(L, -2) == LUA_TSTRING) {
      std::string key = lua_tostring(L, -2);
      if (globals.find(key) == globals.end()) {
        to_remove.push_back(key);
      }
    }
    lua_pop(L, 1);
  }
  lua_pop(L, 1);

  for (const auto& key : to_remove) {
    lua_pushnil(L);
    lua_setglobal(L, key.c_str());
  }

  // Re-open the safe built-in libraries so any sub-table mutation
  // done by the script (`math.huge = 0`, `string.format = nil`, ...) is
  // wiped before the next borrow. This is the effective equivalent of
  // deep-cloning the baseline tables — luaopen_* allocates fresh table
  // instances on every call and registers fresh C closures into them.
  // Cheaper than maintaining a cross-state native deep-clone and avoids
  // the Lua-to-C-state mapping headache (registry refs are per-state).
  static const struct {
    const char* name;
    lua_CFunction fn;
  } kSafeLibs[] = {
      {"", luaopen_base},
      {LUA_TABLIBNAME, luaopen_table},
      {LUA_STRLIBNAME, luaopen_string},
      {LUA_MATHLIBNAME, luaopen_math},
  };
  for (const auto& lib : kSafeLibs) {
    lua_pushcfunction(L, lib.fn);
    lua_pushstring(L, lib.name);
    lua_call(L, 1, 0);
  }
  // Re-strip the filesystem-exposing globals the LuaVM ctor stripped,
  // because luaopen_base re-installs them.
  lua_pushnil(L);
  lua_setglobal(L, "dofile");
  lua_pushnil(L);
  lua_setglobal(L, "loadfile");

  // Restore non-builtin baseline globals from the captured snapshot.
  // The builtin tables (`string`, `table`, `math`, base lib functions on
  // `_G`) have just been reset above; overwriting them from the snapshot
  // would restore the *polluted* references the borrow had, defeating
  // the deep-clone. We free those refs but skip the setglobal.
  //
  // Stored as a sorted constexpr array of string_view + std::binary_search:
  // every release_vm hits this list, and the prior std::set required heap
  // allocations of every entry the first time the function ran.
  static constexpr std::string_view kSkipBuiltins[] = {
      // sorted lexicographically — binary_search relies on this
      "_G",     "_VERSION", "assert",     "collectgarbage", "error",  "getfenv", "getmetatable",
      "ipairs", "load",     "loadstring", "math",           "next",   "pairs",   "pcall",
      "print",  "rawequal", "rawget",     "rawset",         "select", "setfenv", "setmetatable",
      "string", "table",    "tonumber",   "tostring",       "type",   "unpack",  "xpcall",
  };
  for (const auto& [key, ref] : borrow_snap) {
    if (!std::binary_search(std::begin(kSkipBuiltins), std::end(kSkipBuiltins), std::string_view(key))) {
      lua_rawgeti(L, LUA_REGISTRYINDEX, ref);
      lua_setglobal(L, key.c_str());
    }
    luaL_unref(L, LUA_REGISTRYINDEX, ref);
  }
}

LuaVM::LuaVM() {
  L_ = luaL_newstate();
  if (!L_) {
    throw ExecutionError("failed to create Lua state");
  }

  // Sandbox: Only load safe standard libraries: base (including coroutine etc if opened via those, but here
  // base, table, string, math) To match Go's SkipOpenLibs: true, we open libraries individually using
  // standard luaopen_* functions.
  lua_pushcfunction(L_, luaopen_base);
  lua_pushstring(L_, "");
  lua_call(L_, 1, 0);

  lua_pushcfunction(L_, luaopen_table);
  lua_pushstring(L_, LUA_TABLIBNAME);
  lua_call(L_, 1, 0);

  lua_pushcfunction(L_, luaopen_string);
  lua_pushstring(L_, LUA_STRLIBNAME);
  lua_call(L_, 1, 0);

  lua_pushcfunction(L_, luaopen_math);
  lua_pushstring(L_, LUA_MATHLIBNAME);
  lua_call(L_, 1, 0);

  // Remove potentially filesystem-accessing functions
  lua_pushnil(L_);
  lua_setglobal(L_, "dofile");
  lua_pushnil(L_);
  lua_setglobal(L_, "loadfile");
}

LuaVM::~LuaVM() {
  if (L_) {
    lua_close(L_);
  }
}

void LuaVM::load_script(const std::string& code, const std::string& op_name) {
  if (luaL_dostring(L_, code.c_str()) != 0) {
    std::string err = lua_tostring(L_, -1);
    lua_pop(L_, 1);
    throw ExecutionError(op_name, "lua: failed to load script: " + err);
  }
}

void LuaVM::to_lua(const JsonValue& value) {
  if (value.is_null()) {
    lua_pushnil(L_);
  } else if (value.is_bool()) {
    lua_pushboolean(L_, value.as_bool() ? 1 : 0);
  } else if (value.is_number()) {
    lua_pushnumber(L_, value.as_number());
  } else if (value.is_string()) {
    const auto& s = value.as_string();
    lua_pushlstring(L_, s.c_str(), s.size());
  } else if (value.is_array()) {
    const auto& arr = value.as_array();
    lua_createtable(L_, static_cast<int>(arr.size()), 0);
    for (std::size_t i = 0; i < arr.size(); ++i) {
      to_lua(arr[i]);
      lua_rawseti(L_, -2, static_cast<int>(i + 1));
    }
  } else if (value.is_object()) {
    const auto& obj = value.as_object();
    lua_createtable(L_, 0, static_cast<int>(obj.size()));
    for (const auto& [k, v] : obj) {
      lua_pushlstring(L_, k.c_str(), k.size());
      to_lua(v);
      lua_rawset(L_, -3);
    }
  }
}

JsonValue LuaVM::from_lua(int index) {
  int t = lua_type(L_, index);
  switch (t) {
    case LUA_TNIL:
      return JsonValue();
    case LUA_TBOOLEAN:
      return JsonValue(static_cast<bool>(lua_toboolean(L_, index)));
    case LUA_TNUMBER:
      return JsonValue(lua_tonumber(L_, index));
    case LUA_TSTRING: {
      std::size_t len;
      const char* s = lua_tolstring(L_, index, &len);
      return JsonValue(std::string(s, len));
    }
    case LUA_TTABLE: {
      int abs_idx = (index > 0) ? index : lua_gettop(L_) + index + 1;
      int len = static_cast<int>(lua_objlen(L_, abs_idx));
      if (len > 0) {
        std::vector<JsonValue> arr;
        arr.reserve(static_cast<std::size_t>(len));
        for (int i = 1; i <= len; ++i) {
          lua_rawgeti(L_, abs_idx, i);
          arr.push_back(from_lua(-1));
          lua_pop(L_, 1);
        }
        return JsonValue(std::move(arr));
      }
      std::map<std::string, JsonValue> obj;
      lua_pushnil(L_);
      while (lua_next(L_, abs_idx) != 0) {
        if (lua_type(L_, -2) == LUA_TSTRING) {
          std::string key = lua_tostring(L_, -2);
          obj[key] = from_lua(-1);
        }
        lua_pop(L_, 1);
      }
      if (obj.empty()) {
        return JsonValue(std::vector<JsonValue>{});
      }
      return JsonValue(std::move(obj));
    }
    default:
      return JsonValue();
  }
}

void LuaVM::set_global(const std::string& name, const JsonValue& value) {
  to_lua(value);
  lua_setglobal(L_, name.c_str());
}

void LuaVM::set_global_table(const std::string& name, const std::vector<JsonValue>& values) {
  lua_createtable(L_, static_cast<int>(values.size()), 0);
  for (std::size_t i = 0; i < values.size(); ++i) {
    to_lua(values[i]);
    lua_rawseti(L_, -2, static_cast<int>(i + 1));
  }
  lua_setglobal(L_, name.c_str());
}

std::vector<JsonValue> LuaVM::call_function(const std::string& func_name, int nret,
                                            const std::string& op_name) {
  lua_getglobal(L_, func_name.c_str());
  if (lua_type(L_, -1) != LUA_TFUNCTION) {
    lua_pop(L_, 1);
    throw ExecutionError(op_name, "lua: function \"" + func_name + "\" not defined in script");
  }
  if (lua_pcall(L_, 0, nret, 0) != 0) {
    std::string err = lua_tostring(L_, -1);
    lua_pop(L_, 1);
    throw ExecutionError(op_name, "lua: " + err);
  }
  std::vector<JsonValue> results;
  results.reserve(static_cast<std::size_t>(nret));
  for (int j = 0; j < nret; ++j) {
    int idx = -(nret - j);
    if (lua_type(L_, idx) == LUA_TNUMBER) {
      double d = lua_tonumber(L_, idx);
      if (std::isnan(d)) {
        lua_pop(L_, nret);
        throw ExecutionError(op_name, "lua returned NaN");
      }
    }
    results.push_back(from_lua(idx));
  }
  lua_pop(L_, nret);
  return results;
}

}  // namespace lua
}  // namespace pine
