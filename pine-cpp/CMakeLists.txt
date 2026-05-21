cmake_minimum_required(VERSION 3.20)
project(pine_cpp_mvp LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# --- LuaJIT ---
# Search common install locations (linuxbrew, system apt, /usr/local).
find_library(LUAJIT_LIB NAMES luajit-5.1
    HINTS /home/linuxbrew/.linuxbrew/lib /usr/local/lib /usr/lib /usr/lib/x86_64-linux-gnu)
find_path(LUAJIT_INCLUDE NAMES luajit.h
    HINTS /home/linuxbrew/.linuxbrew/include/luajit-2.1
          /usr/local/include/luajit-2.1
          /usr/include/luajit-2.1)
if(NOT LUAJIT_LIB OR NOT LUAJIT_INCLUDE)
    message(FATAL_ERROR "LuaJIT not found. Install with: brew install luajit OR apt install libluajit-5.1-dev")
endif()
message(STATUS "LuaJIT lib: ${LUAJIT_LIB}")
message(STATUS "LuaJIT include: ${LUAJIT_INCLUDE}")

add_library(pine_cpp_core
    src/config/json.cpp
    src/config/config.cpp
    src/registry/registry.cpp
    src/dag/dag.cpp
    src/runtime/engine.cpp
    src/render/render.cpp
    src/lua/lua_bridge.cpp
    src/redis/redis_client.cpp
    operators/filter/filter_truncate.cpp
    operators/recall/recall_static.cpp
    operators/reorder/reorder_sort.cpp
    operators/transform/transform_copy.cpp
)

target_include_directories(pine_cpp_core PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_include_directories(pine_cpp_core PRIVATE ${LUAJIT_INCLUDE} ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(pine_cpp_core PUBLIC ${LUAJIT_LIB})

target_compile_options(pine_cpp_core PRIVATE -Wall -Wextra -Wpedantic)

add_executable(pineapple-run cmd/pineapple-run/main.cpp)
target_link_libraries(pineapple-run PRIVATE pine_cpp_core)

add_executable(pineapple-render-dag cmd/pineapple-render-dag/main.cpp)
target_link_libraries(pineapple-render-dag PRIVATE pine_cpp_core)

add_executable(pineapple-codegen cmd/pineapple-codegen/main.cpp)
target_link_libraries(pineapple-codegen PRIVATE pine_cpp_core)

add_executable(pineapple-server cmd/pineapple-server/main.cpp src/server/server.cpp)
target_include_directories(pineapple-server PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(pineapple-server PRIVATE pine_cpp_core pthread)
