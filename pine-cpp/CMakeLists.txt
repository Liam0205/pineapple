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
    src/config/config.cpp
    src/registry/registry.cpp
    src/dag/dag.cpp
    src/dataframe/column.cpp
    src/dataframe/column_store.cpp
    src/dataframe/column_frame.cpp
    src/runtime/engine.cpp
    src/runtime/metrics_nop.cpp
    src/resource/resource.cpp
    src/render/render.cpp
    src/lua/lua_bridge.cpp
    src/lua/lua_pool.cpp
    src/redis/redis_client.cpp
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
)

target_include_directories(pine_cpp_core PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_include_directories(pine_cpp_core PRIVATE ${LUAJIT_INCLUDE} ${CURL_INCLUDE} ${CMAKE_CURRENT_SOURCE_DIR}/src ${CMAKE_CURRENT_SOURCE_DIR})
target_link_libraries(pine_cpp_core PUBLIC ${LUAJIT_LIB} ${CURL_LIB})

target_compile_options(pine_cpp_core PRIVATE -Wall -Wextra -Wpedantic)

option(PINE_CPP_WERROR "Treat warnings as errors (strict CI mode)" OFF)
if(PINE_CPP_WERROR)
    target_compile_options(pine_cpp_core PRIVATE -Werror)
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
        tests/test_column_frame.cpp
        tests/test_register_operator.cpp
        tests/test_metrics.cpp
        tests/test_resource.cpp
        tests/test_http_metrics.cpp
        tests/test_http_stats.cpp
        tests/test_metrics_aware.cpp
        tests/test_stats_provider.cpp
        tests/test_remote_pineapple.cpp
    )
    target_sources(pine_cpp_tests PRIVATE src/server/http_metrics.cpp src/server/http_stats.cpp)
    target_link_libraries(pine_cpp_tests PRIVATE pine_cpp_core doctest::doctest)
    target_include_directories(pine_cpp_tests PRIVATE
        ${CMAKE_CURRENT_SOURCE_DIR}/src
        ${CMAKE_CURRENT_SOURCE_DIR}/include)
    pine_cpp_target_warnings(pine_cpp_tests)

    enable_testing()
    add_test(NAME pine_cpp_tests COMMAND pine_cpp_tests)
endif()
