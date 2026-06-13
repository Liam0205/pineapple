#!/usr/bin/env bash
# pine-go 性能基准入口。
#
# benchmarks 已拆为独立 go module (pine-go/benchmarks/go.mod),
# 默认走子 module; 传 `./internal/...` 等参数可以指向主 module 内的基准。
#
# 子 module 内的 BenchmarkCalibrated / BenchmarkIsolated 都在 //go:build pine_bench
# 下,所以脚本默认带 pine_bench tag,确保 `make bench` 与 `scripts/go-bench.sh`
# 不会静默跳过这些基准。后端对照用 TAGS=lua_gopher 追加(见 bench-lua-backends.sh)。
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

BENCH="${1:-./...}"
shift 2>/dev/null || true

if [[ "$BENCH" == "./..." || "$BENCH" == "./benchmarks"* ]]; then
  cd "$REPO_ROOT/pine-go/benchmarks"
  TARGET="./..."
else
  cd "$REPO_ROOT/pine-go"
  TARGET="$BENCH"
fi

# TAGS 追加在 pine_bench 之上,保留 -tags='pine_bench lua_gopher' 这种组合的可能。
TAGS="${TAGS:-}"
go test -tags="pine_bench${TAGS:+ $TAGS}" -bench=. -benchmem -count=3 -run='^$' "$TARGET" "$@"
