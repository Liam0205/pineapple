#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT/pine-go"

go run ./cmd/pineapple-codegen \
  -output "$REPO_ROOT/apple_generated" \
  -doc-dir "$REPO_ROOT/doc/operators" \
  -operators-dir operators \
  "$@"

echo "==> Codegen complete."
echo "    apple_generated/ and doc/operators/ updated."
