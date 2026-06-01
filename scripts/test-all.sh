#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "==> [1/3] Go tests"
"$REPO_ROOT/scripts/go-test.sh"

echo
echo "==> [2/3] Python tests (apple)"
if [[ -f "$REPO_ROOT/.venv/bin/activate" ]]; then
  source "$REPO_ROOT/.venv/bin/activate"
fi
python3 -m pytest "$REPO_ROOT/apple/tests/" -v "$@"

echo
echo "==> [3/3] Java tests"
"$REPO_ROOT/scripts/java-test.sh"

echo
echo "==> All test suites passed."
