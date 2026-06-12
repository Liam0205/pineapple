#!/usr/bin/env bash
# pine-go 性能基准入口。
#
# benchmarks 已拆为独立 go module (pine-go/benchmarks/go.mod),
# 默认走子 module; 传 `./internal/...` 等参数可以指向主 module 内的基准。
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

go test -bench=. -benchmem -count=3 -run='^$' "$TARGET" "$@"
