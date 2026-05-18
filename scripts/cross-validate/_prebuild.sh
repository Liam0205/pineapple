#!/usr/bin/env bash
# Pre-build Go and Java binaries for cross-validation.
# Typically sourced by _env.sh, not run directly.

set -euo pipefail

echo "==> Pre-building binaries..."

echo "    Building Go CLIs..."
(cd "$REPO_ROOT/pine-go" && go build -o "$WORK_DIR/pineapple-codegen" ./cmd/pineapple-codegen/)
(cd "$REPO_ROOT/pine-go" && go build -o "$WORK_DIR/pineapple-dag" ./cmd/pineapple-dag/)
(cd "$REPO_ROOT/pine-go" && go build -o "$WORK_DIR/pineapple-run" ./cmd/pineapple-run/)
(cd "$REPO_ROOT/pine-go" && go build -o "$WORK_DIR/pineapple-server" ./cmd/pineapple-server/)

echo "    Compiling Java + resolving classpath..."
(cd "$REPO_ROOT/pine-java" && mvn compile -B -q)
export JAVA_CP="$REPO_ROOT/pine-java/target/classes:$(cd "$REPO_ROOT/pine-java" && mvn dependency:build-classpath -B -q -Dmdep.outputFile=/dev/stdout 2>/dev/null | tail -1)"

echo "    Done."
echo
