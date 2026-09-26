#!/usr/bin/env bash
# pine-go benchmark entry point.
#
# benchmarks live in a separate go module (pine-go/benchmarks/go.mod) and that
# submodule is the default; pass an argument such as `./internal/...` to target
# benchmarks inside the main module instead.
#
# BenchmarkCalibrated in the submodule sits behind //go:build pine_bench (while
# BenchmarkIsolated / BenchmarkLuaVsGo / BenchmarkSmallPipeline etc. carry no tag),
# so the script passes the pine_bench tag by default; otherwise `make bench` and
# `scripts/go-bench.sh` would silently skip the Calibrated tier.
# For a backend comparison, add TAGS=lua_gopher (see bench-lua-backends.sh).
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

# TAGS is added on top of pine_bench, so combinations like -tags='pine_bench lua_gopher' still work.
TAGS="${TAGS:-}"
go test -tags="pine_bench${TAGS:+ $TAGS}" -bench=. -benchmem -count=3 -run='^$' "$TARGET" "$@"
