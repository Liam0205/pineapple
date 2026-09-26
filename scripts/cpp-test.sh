#!/usr/bin/env bash
# pine-cpp test entry point: CMake configure + build pine_cpp_tests + ctest.
#
# Follows the "no inline multi-step recipes in the Makefile" rule (see the design
# notes at the top of the top-level Makefile): the actual steps live in
# scripts/*.sh; the Makefile only names and composes them.
#
# Usage:
#   bash scripts/cpp-test.sh [PARALLEL=N]
#
# Environment variables / arguments:
#   PARALLEL  Parallelism passed to cmake --build -j; defaults to 12 (matches
#             $(PARALLEL) in the top-level Makefile). Also accepted as a single
#             `PARALLEL=N` argument, so `make cpp-test PARALLEL=$(nproc)` can
#             forward it directly.
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Resolve PARALLEL: the environment variable first, then a `PARALLEL=N` argument.
PARALLEL="${PARALLEL:-12}"
for arg in "$@"; do
  case "$arg" in
    PARALLEL=*) PARALLEL="${arg#PARALLEL=}" ;;
    *) echo "Unknown arg: $arg" >&2; exit 1 ;;
  esac
done

cd "$REPO_ROOT"

# Debug build with the test target enabled. CMAKE_POLICY_VERSION_MINIMUM matches
# the top-level Makefile, for compatibility with any older CMakeLists
# subdirectories vendored into the repo.
cmake -S pine-cpp -B pine-cpp/build-tests \
    -DCMAKE_BUILD_TYPE=Debug \
    -DPINE_CPP_BUILD_TESTS=ON \
    -DCMAKE_POLICY_VERSION_MINIMUM=3.5
cmake --build pine-cpp/build-tests --target pine_cpp_tests -j"$PARALLEL"
cd pine-cpp/build-tests && ctest --output-on-failure
