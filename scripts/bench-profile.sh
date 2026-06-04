#!/usr/bin/env bash
# Profile pine-cpp with a realistic fixture using perf or heaptrack.
#
# Usage:
#   ./scripts/bench-profile.sh [--fixture realistic_for_you_calibrated]
#       [--requests 3000] [--concurrency 50] [--tool perf|heaptrack]
#       [--output /tmp/bench_profile] [--config path] [--request path]
#
# Options:
#   --fixture     Fixture name (looks for {name}_config.json / {name}_request.json)
#   --config      Override config path directly (skips fixture lookup)
#   --request     Override request path directly (skips fixture lookup)
#   --requests    Number of requests to send (default: 3000)
#   --concurrency Concurrent connections (default: 50)
#   --tool        perf or heaptrack (default: perf)
#   --output      Output directory (default: /tmp/bench_profile)
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
FIXTURE_NAME="realistic_for_you_calibrated_2c4g"
NUM_REQUESTS=3000
CONCURRENCY=50
TOOL="perf"
CUSTOM_CFG=""
CUSTOM_REQ=""
RESOURCE_LIMIT="${BENCH_RESOURCE_LIMIT:-1}"
CPU_LIST="${BENCH_CPU_LIST:-0,1}"
MEM_MAX="${BENCH_MEM_MAX:-4G}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --fixture)           FIXTURE_NAME="$2"; shift 2 ;;
    --requests)          NUM_REQUESTS="$2"; shift 2 ;;
    --concurrency)       CONCURRENCY="$2"; shift 2 ;;
    --tool)              TOOL="$2"; shift 2 ;;
    --output)            OUTPUT_DIR="$2"; shift 2 ;;
    --config)            CUSTOM_CFG="$2"; shift 2 ;;
    --request)           CUSTOM_REQ="$2"; shift 2 ;;
    --no-resource-limit) RESOURCE_LIMIT=0; shift ;;
    --cpu-list)          CPU_LIST="$2"; shift 2 ;;
    --mem-max)           MEM_MAX="$2"; shift 2 ;;
    *)                   echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

# When RESOURCE_LIMIT=1 (default), the server is launched inside a transient
# user-scope cgroup with `MemoryMax=$MEM_MAX` (no swap) + `taskset -c $CPU_LIST`,
# so profiles reflect the constrained env that production benchmarks use.
SERVER_LAUNCH=()
if [[ "$RESOURCE_LIMIT" == "1" ]]; then
  if ! command -v systemd-run >/dev/null 2>&1 || ! command -v taskset >/dev/null 2>&1; then
    echo "Error: systemd-run/taskset required for cgroup limit; pass --no-resource-limit to disable" >&2
    exit 1
  fi
  SERVER_LAUNCH=(systemd-run --user --scope --quiet
    -p "MemoryMax=$MEM_MAX" -p MemorySwapMax=0
    taskset -c "$CPU_LIST")
fi

CFG="${CUSTOM_CFG:-$FIXTURE_SRC/${FIXTURE_NAME}_config.json}"
REQ="${CUSTOM_REQ:-$FIXTURE_SRC/${FIXTURE_NAME}_request.json}"

# Resolve to absolute paths
[[ "$CFG" != /* ]] && CFG="$REPO_ROOT/$CFG"
[[ "$REQ" != /* ]] && REQ="$REPO_ROOT/$REQ"

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
"${SERVER_LAUNCH[@]}" "$SERVER" -config "$CFG" -addr ":$PORT" > "$OUTPUT_DIR/server.log" 2>&1 &
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

# ─── Validate request file ────────────────────────────────────────────
if [[ ! -f "$REQ" ]]; then
  echo "Error: request file not found: $REQ" >&2
  exit 1
fi

# ─── Warmup ──────────────────────────────────────────────────────────
echo "==> Warmup (200 requests)..."
hey -n 200 -c 10 -m POST -H "Content-Type: application/json" \
  -D "$REQ" "http://localhost:$PORT/execute" > /dev/null 2>&1

# ─── Profile ─────────────────────────────────────────────────────────
case "$TOOL" in
  perf)
    echo "==> Profiling with perf (${NUM_REQUESTS} requests, ${CONCURRENCY} concurrent)..."
    PERF_DURATION=$(( NUM_REQUESTS / CONCURRENCY + 30 ))
    rm -f "$OUTPUT_DIR/perf.data"

    perf record -g -F 997 -e cpu-clock -p "$SERVER_PID" \
      -o "$OUTPUT_DIR/perf.data" -- sleep "$PERF_DURATION" &
    PERF_PID=$!
    sleep 1

    hey -n "$NUM_REQUESTS" -c "$CONCURRENCY" -m POST \
      -H "Content-Type: application/json" \
      -D "$REQ" "http://localhost:$PORT/execute" > "$OUTPUT_DIR/hey.txt" 2>&1

    kill "$PERF_PID" 2>/dev/null || true
    wait "$PERF_PID" 2>/dev/null || true

    echo "  ✓ perf.data written to $OUTPUT_DIR/perf.data"
    echo

    # Analysis section — disable pipefail since head/grep may close pipes early
    set +o pipefail

    # Top symbols summary
    echo "==> Top 25 symbols (self time):"
    perf report -i "$OUTPUT_DIR/perf.data" --no-children --stdio -g none 2>/dev/null \
      | grep -E "^\s+[0-9]+\.[0-9]+%" | head -25 \
      | sed 's/pineapple-serve  //' \
      | tee "$OUTPUT_DIR/top_symbols.txt"
    echo

    # Category breakdown
    echo "==> Category breakdown:"
    PERF_LINES=$(perf report -i "$OUTPUT_DIR/perf.data" --no-children --stdio -g none 2>/dev/null \
      | grep -E "^\s+[0-9]+\.[0-9]+%") || true
    _sum_pct() {
      echo "$PERF_LINES" | { grep -iE "$1" || true; } \
        | awk '{gsub(/%/,"",$1); sum+=$1} END{printf "%.2f%%", sum+0}'
    }
    printf "  std::map (Rb_tree):     %s\n" "$(_sum_pct '_Rb_tree')"
    printf "  JSON serialization:     %s\n" "$(_sum_pct 'write_json_value|write_go_string|rapidjson::Writer|go_format_json')"
    printf "  locking (pthread_rw/m): %s\n" "$(_sum_pct 'pthread_rwlock|pthread_mutex')"
    printf "  allocator (jemalloc):   %s\n" "$(_sum_pct 'jemalloc|operator new|operator delete')"
    printf "  variant ops:            %s\n" "$(_sum_pct 'Variant_storage.*_M_reset|variant.*copy_assign|is_null')"
    echo

    set -o pipefail

    # hey benchmark summary
    echo "==> Benchmark results:"
    grep -E "Requests/sec|Total:|50%|99%" "$OUTPUT_DIR/hey.txt" | sed 's/^/  /'
    echo
    if command -v stackcollapse-perf.pl >/dev/null 2>&1; then
      perf script -i "$OUTPUT_DIR/perf.data" | stackcollapse-perf.pl | \
        flamegraph.pl > "$OUTPUT_DIR/flamegraph.svg"
      echo "  ✓ Flamegraph: $OUTPUT_DIR/flamegraph.svg"
    fi

    # Caller-graph report for deeper analysis
    perf report -i "$OUTPUT_DIR/perf.data" --no-children --stdio -g caller 2>/dev/null \
      > "$OUTPUT_DIR/perf_report_callers.txt"
    echo "  ✓ Caller report: $OUTPUT_DIR/perf_report_callers.txt"
    ;;

  heaptrack)
    echo "==> Profiling with heaptrack (${NUM_REQUESTS} requests, ${CONCURRENCY} concurrent)..."
    # Stop current server, restart under heaptrack
    kill -TERM "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true

    heaptrack -o "$OUTPUT_DIR/heaptrack" \
      "${SERVER_LAUNCH[@]}" "$SERVER" -config "$CFG" -addr ":$PORT" > "$OUTPUT_DIR/server.log" 2>&1 &
    SERVER_PID=$!

    for _ in $(seq 1 40); do
      curl -sf "http://localhost:$PORT/health" >/dev/null 2>&1 && break
      sleep 0.25
    done

    hey -n "$NUM_REQUESTS" -c "$CONCURRENCY" -m POST \
      -H "Content-Type: application/json" \
      -D "$REQ" "http://localhost:$PORT/execute" > "$OUTPUT_DIR/hey.txt" 2>&1

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
