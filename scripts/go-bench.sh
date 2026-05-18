#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT/pine-go"

BENCH="${1:-./benchmarks/}"
shift 2>/dev/null || true

go test -bench=. -benchmem -count=3 "$BENCH" "$@"
