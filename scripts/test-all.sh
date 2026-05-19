#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "==> [1/4] Go tests"
"$REPO_ROOT/scripts/go-test.sh"

echo
echo "==> [2/4] Python tests (apple)"
if [[ -f "$REPO_ROOT/.venv/bin/activate" ]]; then
  source "$REPO_ROOT/.venv/bin/activate"
fi
python3 -m pytest "$REPO_ROOT/apple/tests/" -v "$@"

echo
echo "==> [3/4] Pine-Python tests"
(cd "$REPO_ROOT/pine-python" && python3 -m pytest tests/ -v "$@")

echo
echo "==> [4/4] Java tests"
"$REPO_ROOT/scripts/java-test.sh"

echo
echo "==> All test suites passed."
