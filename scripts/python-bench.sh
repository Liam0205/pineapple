#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT/pine-python"

echo "==> Benchmarking pine-python"
python3 -m pytest tests/test_bench.py -v -m benchmark \
  --benchmark-enable \
  --benchmark-sort=name \
  "$@"
