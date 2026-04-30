#!/bin/bash
set -uo pipefail

cd "$(git rev-parse --show-toplevel 2>/dev/null || echo .)"

errors=""

# Python lint
if [ -d "apple" ]; then
  py_out=$(ruff check apple/ 2>&1)
  if [ $? -ne 0 ]; then
    errors+="=== ruff check apple/ ===\n${py_out}\n\n"
  fi
fi

# Go lint
if [ -f "go.mod" ]; then
  go_out=$(golangci-lint run ./... 2>&1)
  if [ $? -ne 0 ]; then
    errors+="=== golangci-lint run ./... ===\n${go_out}\n\n"
  fi
fi

if [ -n "$errors" ]; then
  echo -e "$errors"
  exit 2
fi

exit 0
