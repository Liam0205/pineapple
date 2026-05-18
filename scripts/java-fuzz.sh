#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT/pine-java"

DURATION="${1:-60}"
shift 2>/dev/null || true

echo "==> Fuzzing pine-java (duration: ${DURATION}s)"
JAZZER_FUZZ=1 mvn test -B \
  -Dtest=JazzerFuzzTest \
  -DfailIfNoTests=false \
  -Djunit.jupiter.execution.timeout.default="${DURATION} s" \
  "$@"
