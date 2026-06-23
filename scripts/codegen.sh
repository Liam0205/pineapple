#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $0 [--backend go|java] [extra flags...]

Generate Apple Python operator code and documentation from operator schemas.

Options:
  --backend go|java   Which runtime to use for codegen (default: go)

Go backend flags are passed to pineapple-codegen.
Java backend uses --schema-from-registry by default.

Examples:
  $0
  $0 --backend java
  $0 --backend go -schema-json schema.json
EOF
  exit 1
}

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BACKEND="go"

if [[ "${1:-}" == "--backend" ]]; then
  BACKEND="$2"; shift 2
fi

case "$BACKEND" in
  go)
    cd "$REPO_ROOT/pine-go"
    go run ./cmd/pineapple-codegen \
      -output "$REPO_ROOT/apple_generated" \
      -doc-dir "$REPO_ROOT/doc/operators" \
      -operators-dir operators \
      "$@"
    ;;
  java)
    cd "$REPO_ROOT/pine-java"
    mvn -B -q compile exec:java \
      -Dexec.mainClass="page.liam.pine.Codegen" \
      -Dexec.args="--schema-from-registry -output $REPO_ROOT/apple_generated -doc-dir $REPO_ROOT/doc/operators -ops-dir $REPO_ROOT/pine-java/src/main/java/page/liam/pine/operators $*"
    ;;
  *)
    echo "Unknown backend: $BACKEND" >&2; usage ;;
esac

echo "==> Codegen complete (backend: $BACKEND)."
echo "    apple_generated/ and doc/operators/ updated."
