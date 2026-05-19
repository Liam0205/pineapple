#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT/pine-python"

DURATION="${1:-500}"
shift 2>/dev/null || true

echo "==> Fuzzing pine-python (max_examples: $DURATION)"
python3 -m pytest tests/test_fuzz.py -v -m fuzz \
  --hypothesis-seed=0 \
  -o "hypothesis_settings_max_examples=$DURATION" \
  "$@"
