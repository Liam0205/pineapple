#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT/pine-go"

DURATION="${1:-30s}"
shift 2>/dev/null || true

echo "==> Fuzzing pine-go (duration: $DURATION)"

FUZZ_TARGETS=$(grep -rn "^func Fuzz" --include="*.go" -l . | sort -u)
for f in $FUZZ_TARGETS; do
  pkg=$(dirname "$f")
  funcs=$(grep -oP '^func \KFuzz\w+' "$f")
  for fn in $funcs; do
    echo "--- $pkg :: $fn"
    go test "$pkg" -fuzz="^${fn}$" -fuzztime="$DURATION" "$@" || true
  done
done
