#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

errors=""

echo "==> Python lint (ruff)"
if [[ -d "$REPO_ROOT/apple" ]]; then
  py_out=$(ruff check "$REPO_ROOT/apple/" 2>&1) || errors+="=== ruff ===\n${py_out}\n\n"
fi

echo "==> Go lint (golangci-lint)"
if [[ -f "$REPO_ROOT/pine-go/go.mod" ]]; then
  go_out=$(cd "$REPO_ROOT/pine-go" && golangci-lint run ./... 2>&1) || errors+="=== golangci-lint ===\n${go_out}\n\n"
fi

echo "==> Java lint (checkstyle)"
if [[ -f "$REPO_ROOT/pine-java/pom.xml" ]]; then
  java_out=$(cd "$REPO_ROOT/pine-java" && mvn checkstyle:check -B -q 2>&1) || errors+="=== checkstyle ===\n${java_out}\n\n"
fi

if [[ -n "$errors" ]]; then
  echo
  echo "Lint failures:" >&2
  echo -e "$errors" >&2
  exit 1
fi

echo
echo "==> All linters passed."
