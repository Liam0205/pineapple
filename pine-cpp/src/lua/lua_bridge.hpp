#pragma once

#include "pine/pine.hpp"

struct lua_State;

namespace pine {
namespace lua {

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

private:
    void push_value(const JsonValue& value);
    JsonValue to_value(int index);
    lua_State* L_;
};

}  // namespace lua
}  // namespace pine
