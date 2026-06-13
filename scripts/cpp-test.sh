#!/usr/bin/env bash
# pine-cpp 测试入口:CMake configure + 编译 pine_cpp_tests + ctest。
#
# 与"Makefile 不内联多步命令"原则对齐(顶层 Makefile 自陈第 6 行):
# 具体步骤位于 scripts/*.sh,Makefile 只做命名与组合。
#
# 用法:
#   bash scripts/cpp-test.sh [PARALLEL=N]
#
# 环境变量 / 参数:
#   PARALLEL  传给 cmake --build -j 的并发数;默认 12(对照顶层 Makefile $(PARALLEL))。
#             也接受 `PARALLEL=N` 形式作为单参数(便于 `make cpp-test PARALLEL=$(nproc)`
#             直接转发)。
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# 解析 PARALLEL,环境变量优先,然后扫单参数 `PARALLEL=N` 兼容形式。
PARALLEL="${PARALLEL:-12}"
for arg in "$@"; do
  case "$arg" in
    PARALLEL=*) PARALLEL="${arg#PARALLEL=}" ;;
    *) echo "Unknown arg: $arg" >&2; exit 1 ;;
  esac
done

cd "$REPO_ROOT"

# Debug 配置 + 启用测试 target;CMAKE_POLICY_VERSION_MINIMUM 同顶层 Makefile,
# 兼容仓库内打的旧版 CMakeLists 子目录(若有)。
cmake -S pine-cpp -B pine-cpp/build-tests \
    -DCMAKE_BUILD_TYPE=Debug \
    -DPINE_CPP_BUILD_TESTS=ON \
    -DCMAKE_POLICY_VERSION_MINIMUM=3.5
cmake --build pine-cpp/build-tests --target pine_cpp_tests -j"$PARALLEL"
cd pine-cpp/build-tests && ctest --output-on-failure
