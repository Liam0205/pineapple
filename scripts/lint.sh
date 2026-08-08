#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

errors=""

echo "==> Python lint (ruff)"
if [[ -d "$REPO_ROOT/apple" ]]; then
  py_out=$(ruff check "$REPO_ROOT/apple/" 2>&1) || errors+="=== ruff (apple) ===\n${py_out}\n\n"
fi

echo "==> Go lint (golangci-lint)"
if [[ -f "$REPO_ROOT/pine-go/go.mod" ]]; then
  go_out=$(cd "$REPO_ROOT/pine-go" && golangci-lint run ./... 2>&1) || errors+="=== golangci-lint ===\n${go_out}\n\n"
fi

echo "==> Java lint (checkstyle)"
if [[ -f "$REPO_ROOT/pine-java/pom.xml" ]]; then
  java_out=$(cd "$REPO_ROOT/pine-java" && mvn checkstyle:check -B -q 2>&1) || errors+="=== checkstyle ===\n${java_out}\n\n"
fi

# Cross-runtime metric Help parity. Cheap (pure text scan, no build) and it
# guards a property nothing else can see: `# HELP` never reaches any output, so
# drift here is invisible to cross-validate. See issue #193.
help_out=$(python3 "$REPO_ROOT/scripts/check-metrics-help-parity.py" 2>&1) \
  || errors+="=== metrics Help parity ===\n${help_out}\n\n"

if [[ -n "$errors" ]]; then
  echo
  echo "Lint failures:" >&2
  echo -e "$errors" >&2
  exit 1
fi

echo
echo "==> All linters passed."
