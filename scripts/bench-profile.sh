#!/usr/bin/env bash
# Profile pine-cpp with a realistic fixture using perf or heaptrack.
#
# Usage:
#   ./scripts/bench-profile.sh [--fixture realistic_for_you] [--requests 5000]
#       [--concurrency 20] [--tool perf|heaptrack] [--output /tmp/profile]
#
# Prerequisites:
#   - perf (linux-tools-$(uname -r)) or heaptrack
#   - hey: go install github.com/rakyll/hey@latest
#   - cmake + build-essential + libluajit
#
# Output:
#   perf:      <output>/flamegraph.svg + perf.data
#   heaptrack: <output>/heaptrack.gz

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FIXTURE_SRC="$REPO_ROOT/fixtures/benchmarks"
OUTPUT_DIR="/tmp/bench_profile"
FIXTURE_NAME="realistic_for_you"
NUM_REQUESTS=5000
CONCURRENCY=20
TOOL="perf"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --fixture)     FIXTURE_NAME="$2"; shift 2 ;;
    --requests)    NUM_REQUESTS="$2"; shift 2 ;;
    --concurrency) CONCURRENCY="$2"; shift 2 ;;
    --tool)        TOOL="$2"; shift 2 ;;
    --output)      OUTPUT_DIR="$2"; shift 2 ;;
    *)             echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

CFG="$FIXTURE_SRC/${FIXTURE_NAME}_config.json"
REQ="$FIXTURE_SRC/${FIXTURE_NAME}_request.json"

if [[ ! -f "$CFG" ]]; then
  echo "Error: fixture config not found: $CFG" >&2
  exit 1
fi

mkdir -p "$OUTPUT_DIR"

# ─── Build C++ in Release with debug info ─────────────────────────────
echo "==> Building pine-cpp (RelWithDebInfo)..."
CPP_BUILD="$REPO_ROOT/pine-cpp/build"
mkdir -p "$CPP_BUILD"
(cd "$CPP_BUILD" && cmake .. -DCMAKE_BUILD_TYPE=RelWithDebInfo -DCMAKE_POLICY_VERSION_MINIMUM=3.5 >/dev/null 2>&1 \
  && cmake --build . -j2 --target pineapple-server 2>&1 | tail -1)
SERVER="$CPP_BUILD/pineapple-server"
echo "  ✓ Built: $SERVER"

# ─── Start server ─────────────────────────────────────────────────────
PORT=19200
echo "==> Starting server on :$PORT..."
"$SERVER" -config "$CFG" -addr ":$PORT" > "$OUTPUT_DIR/server.log" 2>&1 &
SERVER_PID=$!

cleanup() {
  kill -TERM "$SERVER_PID" 2>/dev/null || true
  wait "$SERVER_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

for _ in $(seq 1 40); do
  curl -sf "http://localhost:$PORT/health" >/dev/null 2>&1 && break
  sleep 0.25
done

if ! curl -sf "http://localhost:$PORT/health" >/dev/null 2>&1; then
  echo "Error: server failed to start" >&2
  tail -20 "$OUTPUT_DIR/server.log" >&2
  exit 1
fi
echo "  ✓ Server ready (PID $SERVER_PID)"

# ─── Prepare request body ─────────────────────────────────────────────
if [[ -f "$REQ" ]]; then
  REQ_BODY=$(cat "$REQ")
else
  REQ_BODY='{"common":{},"items":[]}'
fi

# ─── Warmup ──────────────────────────────────────────────────────────
echo "==> Warmup (200 requests)..."
hey -n 200 -c 10 -m POST -H "Content-Type: application/json" \
  -d "$REQ_BODY" "http://localhost:$PORT/execute" > /dev/null 2>&1

# ─── Profile ─────────────────────────────────────────────────────────
case "$TOOL" in
  perf)
    echo "==> Profiling with perf (${NUM_REQUESTS} requests, ${CONCURRENCY} concurrent)..."
    perf record -g -p "$SERVER_PID" -o "$OUTPUT_DIR/perf.data" -- \
      sleep 0 &
    PERF_PID=$!

    # Actually attach perf to the server while hey sends load
    perf record -g -p "$SERVER_PID" -o "$OUTPUT_DIR/perf.data" &
    PERF_PID=$!
    sleep 0.5

    hey -n "$NUM_REQUESTS" -c "$CONCURRENCY" -m POST \
      -H "Content-Type: application/json" \
      -d "$REQ_BODY" "http://localhost:$PORT/execute" > "$OUTPUT_DIR/hey.txt" 2>&1

    kill -INT "$PERF_PID" 2>/dev/null || true
    wait "$PERF_PID" 2>/dev/null || true

    echo "  ✓ perf.data written to $OUTPUT_DIR/perf.data"

    # Generate flamegraph if stackcollapse-perf.pl is available
    if command -v stackcollapse-perf.pl >/dev/null 2>&1; then
      perf script -i "$OUTPUT_DIR/perf.data" | stackcollapse-perf.pl | \
        flamegraph.pl > "$OUTPUT_DIR/flamegraph.svg"
      echo "  ✓ Flamegraph: $OUTPUT_DIR/flamegraph.svg"
    elif command -v perf >/dev/null 2>&1; then
      perf report -i "$OUTPUT_DIR/perf.data" --stdio > "$OUTPUT_DIR/perf_report.txt" 2>/dev/null
      echo "  ✓ perf report: $OUTPUT_DIR/perf_report.txt"
      echo "  (Install FlameGraph tools for SVG output)"
    fi
    ;;

  heaptrack)
    echo "==> Profiling with heaptrack (${NUM_REQUESTS} requests, ${CONCURRENCY} concurrent)..."
    # Stop current server, restart under heaptrack
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true

    heaptrack -o "$OUTPUT_DIR/heaptrack" \
      "$SERVER" -config "$CFG" -addr ":$PORT" > "$OUTPUT_DIR/server.log" 2>&1 &
    SERVER_PID=$!

    for _ in $(seq 1 40); do
      curl -sf "http://localhost:$PORT/health" >/dev/null 2>&1 && break
      sleep 0.25
    done

    hey -n "$NUM_REQUESTS" -c "$CONCURRENCY" -m POST \
      -H "Content-Type: application/json" \
      -d "$REQ_BODY" "http://localhost:$PORT/execute" > "$OUTPUT_DIR/hey.txt" 2>&1

    kill -TERM "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true

    echo "  ✓ heaptrack data: $OUTPUT_DIR/heaptrack.*.gz"
    echo "  Analyze with: heaptrack_gui $OUTPUT_DIR/heaptrack.*.gz"
    ;;

  *)
    echo "Error: unknown tool '$TOOL'. Use 'perf' or 'heaptrack'." >&2
    exit 1
    ;;
esac

echo
echo "==> Done. Output in: $OUTPUT_DIR/"
