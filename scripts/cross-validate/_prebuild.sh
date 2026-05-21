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

echo "    Building C++ CLIs..."
if [[ -d "$REPO_ROOT/pine-cpp" ]]; then
  mkdir -p "$REPO_ROOT/pine-cpp/build"
  (cd "$REPO_ROOT/pine-cpp/build" && cmake .. -DCMAKE_BUILD_TYPE=Release >/dev/null 2>&1 && make -j"$(nproc 2>/dev/null || echo 4)" >/dev/null 2>&1) && \
    cp "$REPO_ROOT/pine-cpp/build/pineapple-run" "$WORK_DIR/pineapple-run-cpp" 2>/dev/null && \
    cp "$REPO_ROOT/pine-cpp/build/pineapple-render-dag" "$WORK_DIR/pineapple-dag-cpp" 2>/dev/null && \
    cp "$REPO_ROOT/pine-cpp/build/pineapple-codegen" "$WORK_DIR/pineapple-codegen-cpp" 2>/dev/null && \
    cp "$REPO_ROOT/pine-cpp/build/pineapple-server" "$WORK_DIR/pineapple-server-cpp" 2>/dev/null && \
    export CPP_RUN="$WORK_DIR/pineapple-run-cpp" && \
    export CPP_DAG="$WORK_DIR/pineapple-dag-cpp" && \
    export CPP_CODEGEN="$WORK_DIR/pineapple-codegen-cpp" && \
    export CPP_SERVER="$WORK_DIR/pineapple-server-cpp" || \
    echo "    (C++ build skipped or failed — C++ parity checks will be skipped)"
fi

echo "    Done."
echo
