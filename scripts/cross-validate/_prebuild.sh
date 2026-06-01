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
(cd "$REPO_ROOT/pine-go" && go build -o "$WORK_DIR/pine-cause-chain-probe" ./cmd/pine-cause-chain-probe/)

echo "    Compiling Java + resolving classpath..."
(cd "$REPO_ROOT/pine-java" && mvn compile -B -q)
export JAVA_CP="$REPO_ROOT/pine-java/target/classes:$(cd "$REPO_ROOT/pine-java" && mvn dependency:build-classpath -B -q -Dmdep.outputFile=/dev/stdout 2>/dev/null | tail -1)"

echo "    Building C++ CLIs..."
# Surface failures instead of silently skipping the C++ binaries.
# Previously this block redirected cmake/make output to /dev/null and
# fell back to printing "C++ build skipped or failed" without setting a
# non-zero exit. That meant CI ran cross-validation with the four CPP_*
# env vars unset, and every C++ parity check was no-op'd while the
# overall pipeline reported success. 3 rounds of code review caught
# the symptom in different sections before the cause was named.
if [[ -d "$REPO_ROOT/pine-cpp" ]]; then
    CPP_BUILD_LOG="$WORK_DIR/cpp-build.log"
    mkdir -p "$REPO_ROOT/pine-cpp/build"
    if ! (cd "$REPO_ROOT/pine-cpp/build" \
            && cmake .. -DCMAKE_BUILD_TYPE=Release -DPINE_BUILD_BENCH_STUBS=OFF 2>&1 \
            && make -j"$(nproc 2>/dev/null || echo 4)" 2>&1) | tee "$CPP_BUILD_LOG" >/dev/null; then
        echo "    C++ build FAILED — last 50 lines of $CPP_BUILD_LOG:" >&2
        tail -n 50 "$CPP_BUILD_LOG" >&2
        exit 1
    fi
    cp "$REPO_ROOT/pine-cpp/build/pineapple-run" "$WORK_DIR/pineapple-run-cpp"
    cp "$REPO_ROOT/pine-cpp/build/pineapple-render-dag" "$WORK_DIR/pineapple-dag-cpp"
    cp "$REPO_ROOT/pine-cpp/build/pineapple-codegen" "$WORK_DIR/pineapple-codegen-cpp"
    cp "$REPO_ROOT/pine-cpp/build/pineapple-server" "$WORK_DIR/pineapple-server-cpp"
    cp "$REPO_ROOT/pine-cpp/build/pineapple-cause-chain-probe" "$WORK_DIR/pineapple-cause-chain-probe-cpp"
    export CPP_RUN="$WORK_DIR/pineapple-run-cpp"
    export CPP_DAG="$WORK_DIR/pineapple-dag-cpp"
    export CPP_CODEGEN="$WORK_DIR/pineapple-codegen-cpp"
    export CPP_SERVER="$WORK_DIR/pineapple-server-cpp"
    export CPP_CAUSE_CHAIN_PROBE="$WORK_DIR/pineapple-cause-chain-probe-cpp"
fi

echo "    Done."
echo
