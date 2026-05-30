cmake_minimum_required(VERSION 3.20)
project(pine_cpp_mvp LANGUAGES CXX)

# Bumped from C++20 to C++23 for std::stacktrace (PanicError detail).
set(CMAKE_CXX_STANDARD 23)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Detect the linker library needed for std::stacktrace. GCC 13+ ships
# the implementation in libstdc++exp.a; older toolchains use the legacy
# name libstdc++_libbacktrace. Both are independent backend wrappers
# around libbacktrace. We probe at configure time and skip stacktrace
# linkage if neither is available — PanicError still works, just without
# the stack field.
include(CheckLibraryExists)
include(CheckCXXSourceCompiles)
set(PINE_STACKTRACE_LIB "")
foreach(lib stdc++exp stdc++_libbacktrace)
  set(CMAKE_REQUIRED_LIBRARIES "${lib}")
  check_cxx_source_compiles(
    "#include <stacktrace>
     int main(){ auto t = std::stacktrace::current(); return t.size() == 0 ? 1 : 0; }"
    PINE_HAS_${lib})
  unset(CMAKE_REQUIRED_LIBRARIES)
  if(PINE_HAS_${lib})
    set(PINE_STACKTRACE_LIB "${lib}")
    add_compile_definitions(PINE_HAS_STACKTRACE=1)
    message(STATUS "std::stacktrace available via lib${lib}")
    break()
  endif()
endforeach()
if(PINE_STACKTRACE_LIB STREQUAL "")
  message(WARNING "std::stacktrace not available — PanicError will not carry stack frames")
endif()

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

# --- jemalloc (optional, replaces ptmalloc2 for lower malloc contention) ---
# ON by default for production/benchmark builds. Disable with -DPINE_USE_JEMALLOC=OFF for sanitizer builds.
option(PINE_USE_JEMALLOC "Link jemalloc as the allocator" ON)
if(PINE_USE_JEMALLOC)
    find_library(JEMALLOC_LIB NAMES jemalloc
        HINTS /home/linuxbrew/.linuxbrew/lib /usr/local/lib /usr/lib /usr/lib/x86_64-linux-gnu)
    if(NOT JEMALLOC_LIB)
        message(WARNING "jemalloc not found, falling back to system allocator. Install with: brew install jemalloc OR apt install libjemalloc-dev")
        set(PINE_USE_JEMALLOC OFF)
    else()
        message(STATUS "jemalloc: ${JEMALLOC_LIB}")
    endif()
endif()

# --- libcurl (for transform_by_remote_pineapple) ---
find_library(CURL_LIB NAMES curl
    HINTS /home/linuxbrew/.linuxbrew/lib /usr/local/lib /usr/lib /usr/lib/x86_64-linux-gnu)
find_path(CURL_INCLUDE NAMES curl/curl.h
    HINTS /home/linuxbrew/.linuxbrew/include /usr/local/include /usr/include)
if(NOT CURL_LIB OR NOT CURL_INCLUDE)
    message(FATAL_ERROR "libcurl not found. Install with: brew install curl OR apt install libcurl4-openssl-dev")
endif()
message(STATUS "libcurl lib: ${CURL_LIB}")
message(STATUS "libcurl include: ${CURL_INCLUDE}")

add_library(pine_cpp_core OBJECT
    src/config/json.cpp
    src/config/json_writer.cpp
    src/config/config.cpp
    src/registry/registry.cpp
    src/dag/dag.cpp
    src/dataframe/column.cpp
    src/dataframe/column_store.cpp
    src/dataframe/column_frame.cpp
    src/dataframe/row_frame.cpp
    src/dataframe/operator_input.cpp
    src/runtime/engine.cpp
    src/runtime/errors.cpp
    src/runtime/metrics_nop.cpp
    src/runtime/thread_pool.cpp
    src/redis/redis_client.cpp
    src/redis/connection_pool.cpp
    src/resource/resource.cpp
    src/render/render.cpp
    src/lua/lua_bridge.cpp
    src/lua/lua_pool.cpp
    operators/_helpers.cpp
    operators/filter/filter_condition.cpp
    operators/filter/filter_paginate.cpp
    operators/filter/filter_truncate.cpp
    operators/merge/merge_dedup.cpp
    operators/observe/observe_log.cpp
    operators/recall/recall_resource.cpp
    operators/recall/recall_static.cpp
    operators/reorder/reorder_shuffle_by_salt.cpp
    operators/reorder/reorder_sort.cpp
    operators/transform/transform_bench_cpu.cpp
    operators/transform/transform_bench_sleep.cpp
    operators/transform/transform_by_lua.cpp
    operators/transform/transform_copy.cpp
    operators/transform/transform_dispatch.cpp
    operators/transform/transform_normalize.cpp
    operators/transform/transform_redis_get.cpp
    operators/transform/transform_redis_set.cpp
    operators/transform/transform_remote_pineapple.cpp
    src/http/http_client.cpp
    src/http/ssrf.cpp
    operators/transform/transform_resource_lookup.cpp
    operators/transform/transform_size.cpp
    operators/bench_stubs.cpp
)

target_include_directories(pine_cpp_core PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_include_directories(pine_cpp_core PRIVATE ${LUAJIT_INCLUDE} ${CURL_INCLUDE} ${CMAKE_CURRENT_SOURCE_DIR}/src ${CMAKE_CURRENT_SOURCE_DIR})
target_link_libraries(pine_cpp_core PUBLIC ${LUAJIT_LIB} ${CURL_LIB})
if(NOT PINE_STACKTRACE_LIB STREQUAL "")
  target_link_libraries(pine_cpp_core PUBLIC ${PINE_STACKTRACE_LIB})
endif()
if(PINE_USE_JEMALLOC)
  target_link_libraries(pine_cpp_core PUBLIC ${JEMALLOC_LIB})
  target_compile_definitions(pine_cpp_core PUBLIC PINE_USE_JEMALLOC=1)
  message(STATUS "pine-cpp: allocator = jemalloc")
else()
  message(STATUS "pine-cpp: allocator = system default (ptmalloc2)")
endif()
# Debug symbols are required for std::stacktrace to resolve source
# file / line numbers; without them the trace prints `at :0`.
target_compile_options(pine_cpp_core PUBLIC -g)

target_compile_options(pine_cpp_core PRIVATE -Wall -Wextra -Wpedantic)

option(PINE_CPP_WERROR "Treat warnings as errors (strict CI mode)" OFF)
if(PINE_CPP_WERROR)
    target_compile_options(pine_cpp_core PRIVATE -Werror)
endif()

option(PINE_USE_HASH_MAP "Use unordered_map for RowFrame/TypedColumnStore field lookups (benchmark A/B)" OFF)
if(PINE_USE_HASH_MAP)
    target_compile_definitions(pine_cpp_core PUBLIC PINE_USE_HASH_MAP=1)
    message(STATUS "pine-cpp: FieldMap = std::unordered_map (PINE_USE_HASH_MAP=ON)")
else()
    message(STATUS "pine-cpp: FieldMap = std::map (default)")
endif()

# Helper to apply per-executable warning flags consistently.
function(pine_cpp_target_warnings tgt)
    target_compile_options(${tgt} PRIVATE -Wall -Wextra -Wpedantic)
    if(PINE_CPP_WERROR)
        target_compile_options(${tgt} PRIVATE -Werror)
    endif()
endfunction()

add_executable(pineapple-run cmd/pineapple-run/main.cpp)
target_link_libraries(pineapple-run PRIVATE pine_cpp_core)
pine_cpp_target_warnings(pineapple-run)

add_executable(pineapple-render-dag cmd/pineapple-render-dag/main.cpp)
target_link_libraries(pineapple-render-dag PRIVATE pine_cpp_core)
pine_cpp_target_warnings(pineapple-render-dag)

add_executable(pineapple-codegen cmd/pineapple-codegen/main.cpp)
target_link_libraries(pineapple-codegen PRIVATE pine_cpp_core)
pine_cpp_target_warnings(pineapple-codegen)

add_executable(pineapple-server cmd/pineapple-server/main.cpp src/server/server.cpp src/server/http_metrics.cpp src/server/http_stats.cpp)
target_include_directories(pineapple-server PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(pineapple-server PRIVATE pine_cpp_core pthread)
pine_cpp_target_warnings(pineapple-server)

add_executable(pineapple-cause-chain-probe cmd/pineapple-cause-chain-probe/main.cpp)
target_link_libraries(pineapple-cause-chain-probe PRIVATE pine_cpp_core)
pine_cpp_target_warnings(pineapple-cause-chain-probe)

# --- RapidJSON (header-only, used for JSON serialization) ---
include(FetchContent)
FetchContent_Declare(
    rapidjson
    GIT_REPOSITORY https://github.com/Tencent/rapidjson.git
    GIT_TAG v1.1.0
)
FetchContent_GetProperties(rapidjson)
if(NOT rapidjson_POPULATED)
    FetchContent_Populate(rapidjson)
endif()
target_include_directories(pine_cpp_core PUBLIC ${rapidjson_SOURCE_DIR}/include)

# --- Tests ---
option(PINE_CPP_BUILD_TESTS "Build pine-cpp unit tests" OFF)
if(PINE_CPP_BUILD_TESTS)
    include(FetchContent)
    FetchContent_Declare(
        doctest
        GIT_REPOSITORY https://github.com/doctest/doctest.git
        GIT_TAG v2.4.11
    )
    FetchContent_MakeAvailable(doctest)

    add_executable(pine_cpp_tests
        tests/test_main.cpp
        tests/test_json.cpp
        tests/test_config.cpp
        tests/test_dag.cpp
        tests/test_engine.cpp
        tests/test_registry.cpp
        tests/test_parallel.cpp
        tests/test_panic.cpp
        tests/test_operator_output.cpp
        tests/test_trace.cpp
        tests/test_column.cpp
        tests/test_format_g.cpp
        tests/test_column_frame.cpp
        tests/test_row_frame.cpp
        tests/test_frame_equivalence.cpp
        tests/test_register_operator.cpp
        tests/test_metrics.cpp
        tests/test_resource.cpp
        tests/test_http_metrics.cpp
        tests/test_http_stats.cpp
        tests/test_metrics_aware.cpp
        tests/test_stats_provider.cpp
        tests/test_remote_pineapple.cpp
        tests/test_error_chain.cpp
    )
    target_sources(pine_cpp_tests PRIVATE src/server/http_metrics.cpp src/server/http_stats.cpp)
    target_link_libraries(pine_cpp_tests PRIVATE pine_cpp_core doctest::doctest)
    target_include_directories(pine_cpp_tests PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/src
        ${CMAKE_CURRENT_SOURCE_DIR}/include
        ${CMAKE_CURRENT_SOURCE_DIR})
    pine_cpp_target_warnings(pine_cpp_tests)

    enable_testing()
    add_test(NAME pine_cpp_tests COMMAND pine_cpp_tests)
endif()
