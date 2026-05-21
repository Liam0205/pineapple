#include "lua_bridge.hpp"

extern "C" {
#include <lua.h>
#include <lauxlib.h>
#include <lualib.h>
}

#include <cmath>

namespace pine {
namespace lua {

LuaVM::LuaVM() {
    L_ = luaL_newstate();
    if (!L_) throw ExecutionError("failed to create Lua state");
    luaL_openlibs(L_);
    lua_pushnil(L_); lua_setglobal(L_, "dofile");
    lua_pushnil(L_); lua_setglobal(L_, "loadfile");
}

LuaVM::~LuaVM() {
    if (L_) lua_close(L_);
}

void LuaVM::load_script(const std::string& code, const std::string& /*op_name*/) {
    if (luaL_dostring(L_, code.c_str()) != 0) {
        std::string err = lua_tostring(L_, -1);
        lua_pop(L_, 1);
        throw ExecutionError("lua: failed to load script: " + err);
    }
}

void LuaVM::push_value(const JsonValue& value) {
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
            push_value(arr[i]);
            lua_rawseti(L_, -2, static_cast<int>(i + 1));
        }
    } else if (value.is_object()) {
        const auto& obj = value.as_object();
        lua_createtable(L_, 0, static_cast<int>(obj.size()));
        for (const auto& [k, v] : obj) {
            lua_pushlstring(L_, k.c_str(), k.size());
            push_value(v);
            lua_rawset(L_, -3);
        }
    }
}

JsonValue LuaVM::to_value(int index) {
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
    default:
        return JsonValue();
    }
}

void LuaVM::set_global(const std::string& name, const JsonValue& value) {
    push_value(value);
    lua_setglobal(L_, name.c_str());
}

void LuaVM::set_global_table(const std::string& name, const std::vector<JsonValue>& values) {
    lua_createtable(L_, static_cast<int>(values.size()), 0);
    for (std::size_t i = 0; i < values.size(); ++i) {
        push_value(values[i]);
        lua_rawseti(L_, -2, static_cast<int>(i + 1));
    }
    lua_setglobal(L_, name.c_str());
}

std::vector<JsonValue> LuaVM::call_function(const std::string& func_name, int nret, const std::string& op_name) {
    lua_getglobal(L_, func_name.c_str());
    if (lua_type(L_, -1) != LUA_TFUNCTION) {
        lua_pop(L_, 1);
        throw ExecutionError("lua: function \"" + func_name + "\" not defined in script");
    }
    if (lua_pcall(L_, 0, nret, 0) != 0) {
        std::string err = lua_tostring(L_, -1);
        lua_pop(L_, 1);
        throw ExecutionError("lua: " + err);
    }
    std::vector<JsonValue> results;
    results.reserve(static_cast<std::size_t>(nret));
    for (int j = 0; j < nret; ++j) {
        int idx = -(nret - j);
        if (lua_type(L_, idx) == LUA_TNUMBER) {
            double d = lua_tonumber(L_, idx);
            if (std::isnan(d)) {
                lua_pop(L_, nret);
                throw ExecutionError("operator \"" + op_name + "\": lua returned NaN");
            }
        }
        results.push_back(to_value(idx));
    }
    lua_pop(L_, nret);
    return results;
}

}  // namespace lua
}  // namespace pine
