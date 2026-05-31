#!/usr/bin/env bash
# Cross-runtime benchmark: pine-{go,java,cpp}
#
# Fixture-driven: loads all *_config.json from fixtures/benchmarks/ by default.
# Each fixture is self-describing (DAG topology, operator mix, storage mode).
#
# Prerequisites:
#   - hey: go install github.com/rakyll/hey@latest
#   - Go, Java 21, cmake + build-essential + libluajit
#
# Usage:
#   ./scripts/bench-cross-runtime.sh [--skip go] [--modes "row,column"]
#       [--requests 1000] [--concurrency 20] [--generate] [--filter "realistic"]
#
# Options:
#   --skip        Runtimes to skip (comma-separated)
#   --modes       Override storage_mode for fixtures that support it (comma-separated)
#   --requests    Number of requests per benchmark run (default: 1000)
#   --concurrency Concurrent connections (default: 20)
#   --generate    Also generate synthetic fixtures via bench-generate-fixtures.py
#   --filter      Only run fixtures whose name matches this substring
#
# Output: bench-results/report-<timestamp>.txt (in repo root, not /tmp)

set -euo pipefail
# Run in its own process group so cleanup can kill the whole group
if [[ "${BENCH_IN_PGRP:-}" != "1" ]]; then
  BENCH_IN_PGRP=1 exec setsid bash "$0" "$@"
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="/tmp/bench_cross_runtime"
RESULTS_DIR="$REPO_ROOT/bench-results"
REPORT="$RESULTS_DIR/report-$(date +%Y%m%d-%H%M%S).txt"
FIXTURE_SRC="$REPO_ROOT/fixtures/benchmarks"

NPROC=$(nproc)
SKIP_RUNTIMES=""
RUNTIMES=(go java cpp)
STORAGE_MODES=()
NUM_REQUESTS=1000
CONCURRENCY=20
GENERATE=false
FILTER=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip)        SKIP_RUNTIMES="$2"; shift 2 ;;
    --modes)       IFS=',' read -ra STORAGE_MODES <<< "$2"; shift 2 ;;
    --requests)    NUM_REQUESTS="$2"; shift 2 ;;
    --concurrency) CONCURRENCY="$2"; shift 2 ;;
    --generate)    GENERATE=true; shift ;;
    --filter)      FILTER="$2"; shift 2 ;;
    *)             echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

mkdir -p "$WORK_DIR" "$RESULTS_DIR"
# Clean up any leftover artifacts from a previous run
rm -f "$WORK_DIR"/*.csv "$WORK_DIR"/*.log "$WORK_DIR"/*.pid
rm -f "$WORK_DIR"/server-go "$WORK_DIR"/server-cpp

# ─── Colors ───────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}==> $*${NC}"; }
ok()    { echo -e "${GREEN}  ✓ $*${NC}"; }
err()   { echo -e "${RED}  ✗ $*${NC}" >&2; }

should_skip() { [[ "$SKIP_RUNTIMES" == *"$1"* ]]; }

# ─── Dependency check ─────────────────────────────────────────────────
if ! command -v hey >/dev/null 2>&1; then
  err "hey not found. Install: go install github.com/rakyll/hey@latest"
  exit 1
fi

# ─── Generate synthetic fixtures (optional) ──────────────────────────
if [[ "$GENERATE" == "true" ]]; then
  info "Generating synthetic fixtures..."
  python3 "$REPO_ROOT/scripts/bench-generate-fixtures.py"
  ok "Synthetic fixtures generated"
fi

# ─── Collect fixture list ─────────────────────────────────────────────
FIXTURES=()
for cfg in "$FIXTURE_SRC"/*_config.json; do
  [[ -f "$cfg" ]] || continue
  name=$(basename "$cfg" _config.json)
  if [[ -n "$FILTER" ]] && [[ "$name" != *"$FILTER"* ]]; then
    continue
  fi
  FIXTURES+=("$name")
done

if [[ ${#FIXTURES[@]} -eq 0 ]]; then
  err "No fixtures found in $FIXTURE_SRC (filter: '${FILTER:-none}')"
  exit 1
fi

info "Fixtures to run: ${#FIXTURES[@]}"
for f in "${FIXTURES[@]}"; do echo "    $f"; done

# ─── Build runtimes ───────────────────────────────────────────────────
info "Building runtimes..."

JAVA_CP=""

if ! should_skip go; then
  info "  Building Go..."
  (cd "$REPO_ROOT/pine-go" && go build -tags pine_bench -o "$WORK_DIR/server-go" ./cmd/pineapple-server/)
  ok "Go built"
fi

if ! should_skip java; then
  info "  Building Java..."
  (cd "$REPO_ROOT/pine-java" && mvn compile -B -q 2>/dev/null)
  JAVA_CP="$REPO_ROOT/pine-java/target/classes:$(cd "$REPO_ROOT/pine-java" && mvn dependency:build-classpath -B -q -Dmdep.outputFile=/dev/stdout 2>/dev/null | tail -1)"
  ok "Java built"
fi

if ! should_skip cpp; then
  info "  Building C++..."
  CPP_BUILD="$REPO_ROOT/pine-cpp/build"
  mkdir -p "$CPP_BUILD"
  (cd "$CPP_BUILD" && cmake .. -DCMAKE_BUILD_TYPE=Release -DCMAKE_POLICY_VERSION_MINIMUM=3.5 -DPINE_USE_JEMALLOC=ON -DPINE_BUILD_BENCH_STUBS=ON >/dev/null 2>&1 \
    && cmake --build . -j2 --target pineapple-server 2>&1 | tail -1)
  cp "$CPP_BUILD/pineapple-server" "$WORK_DIR/server-cpp"
  ok "C++ built"
fi

# ─── Server helpers ───────────────────────────────────────────────────
BASE_PORT=19100
PORT_IDX=0

next_port() { PORT_IDX=$((PORT_IDX + 1)); echo $((BASE_PORT + PORT_IDX)); }

start_server() {
  local runtime="$1" port="$2" config="$3"
  local pid_file="$WORK_DIR/${runtime}.pid"
  local sink="/dev/null"
  # Set BENCH_VERBOSE=1 to capture server logs for debugging startup failures
  [[ "${BENCH_VERBOSE:-}" == "1" ]] && sink="$WORK_DIR/${runtime}.log"
  case "$runtime" in
    java) java -cp "$JAVA_CP" -Dpine.bench=true -Dpine.config="$config" -Dpine.port="$port" \
      page.liam.pine.PineServer >"$sink" 2>&1 & ;;
    go) "$WORK_DIR/server-go" -config "$config" -addr ":$port" >"$sink" 2>&1 & ;;
    cpp) env ${CPP_LD_PRELOAD:+LD_PRELOAD="$CPP_LD_PRELOAD"} \
      "$WORK_DIR/server-cpp" -config "$config" -addr ":$port" >"$sink" 2>&1 & ;;
  esac
  echo $! > "$pid_file"
  for _ in $(seq 1 40); do
    curl -sf "http://localhost:$port/health" >/dev/null 2>&1 && return 0
    sleep 0.25
  done
  err "$runtime server failed to start on :$port"
  [[ "$sink" != "/dev/null" ]] && tail -20 "$sink" >&2 || err "  (rerun with BENCH_VERBOSE=1 to see server logs)"
  return 1
}

stop_server() {
  local runtime="$1"
  local pid_file="$WORK_DIR/${runtime}.pid"
  [[ -f "$pid_file" ]] || return 0
  local pid; pid=$(cat "$pid_file")
  kill -TERM "$pid" 2>/dev/null || true
  for _ in 1 2 3 4 5; do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
  kill -KILL "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  rm -f "$pid_file"
}

cleanup() {
  # Kill all processes in this script's process group (catches hey + servers)
  kill -- -$$ 2>/dev/null || true
  # Also stop any servers tracked by pid files
  for rt in "${RUNTIMES[@]}"; do stop_server "$rt"; done
  rm -f "$WORK_DIR"/server-go "$WORK_DIR"/server-cpp
  rm -f "$WORK_DIR"/*.log "$WORK_DIR"/*.pid
  rm -f "$WORK_DIR"/*.csv
}
trap cleanup EXIT INT TERM

parse_hey() {
  python3 -c "
import csv, math, sys
times, offsets = [], []
for row in csv.DictReader(sys.stdin):
    times.append(float(row['response-time']))
    offsets.append(float(row['offset']))
if not times:
    print('N/A|N/A|N/A|N/A|N/A|N/A')
    sys.exit(0)
n = len(times)
wall = max(o + t for o, t in zip(offsets, times)) - min(offsets)
qps = n / wall if wall > 0 else 0
times.sort()
mean = sum(times) / n
var = sum((t - mean) ** 2 for t in times) / (n - 1) if n > 1 else 0
stddev = math.sqrt(var)
p50 = times[int(n * 0.50)]
p90 = times[int(n * 0.90)]
p99 = times[int(n * 0.99)]
print(f'{qps:.4f}|{mean:.6f}|{stddev:.6f}|{p50:.6f}|{p90:.6f}|{p99:.6f}')
" 2>/dev/null || echo "N/A|N/A|N/A|N/A|N/A|N/A"
}

# ─── Determine storage modes per fixture ─────────────────────────────
# If --modes is specified, override all fixtures. Otherwise, use the
# storage_mode declared in each fixture's config (default: "row").
get_storage_modes() {
  local config_file="$1"
  if [[ ${#STORAGE_MODES[@]} -gt 0 ]]; then
    echo "${STORAGE_MODES[*]}"
    return
  fi
  local mode
  mode=$(python3 -c "import json,sys; c=json.load(open(sys.argv[1])); print(c.get('storage_mode','row'))" "$config_file" 2>/dev/null || echo "row")
  echo "$mode"
}

# ─── Report header ────────────────────────────────────────────────────
{
  echo "═══════════════════════════════════════════════════════════════════"
  echo " Cross-Runtime Benchmark: pine-{go,java,cpp}"
  echo " Date: $(date -Iseconds)"
  echo " Machine: $(uname -n) (${NPROC} cores)"
  echo " Fixtures: ${#FIXTURES[*]} (filter: '${FILTER:-all}')"
  echo " Load: ${NUM_REQUESTS} requests, ${CONCURRENCY} concurrent"
  echo " Skipped: ${SKIP_RUNTIMES:-none}"
  echo "═══════════════════════════════════════════════════════════════════"
  echo
} > "$REPORT"

TABLE_HEADER="  %-8s %-35s %7s %10s %10s %10s %10s %10s %10s\n"

{
  printf "$TABLE_HEADER" "Runtime" "Fixture" "Storage" "QPS" "Mean" "Stddev" "P50" "P90" "P99"
  printf "$TABLE_HEADER" "-------" "-----------------------------------" "-------" "----------" "----------" "----------" "----------" "----------" "----------"
} >> "$REPORT"

# ─── Benchmark loop ──────────────────────────────────────────────────
TOTAL_RUNS=0
for fixture in "${FIXTURES[@]}"; do
  cfg="$FIXTURE_SRC/${fixture}_config.json"
  req="$FIXTURE_SRC/${fixture}_request.json"
  [[ -f "$req" ]] || req=""

  read -ra modes <<< "$(get_storage_modes "$cfg")"

  for mode in "${modes[@]}"; do
    for rt in "${RUNTIMES[@]}"; do
      should_skip "$rt" && continue
      TOTAL_RUNS=$((TOTAL_RUNS + 1))
    done
  done
done

RUN_IDX=0
for fixture in "${FIXTURES[@]}"; do
  cfg="$FIXTURE_SRC/${fixture}_config.json"
  req="$FIXTURE_SRC/${fixture}_request.json"

  if [[ ! -f "$req" ]]; then
    req_body='{"common":{},"items":[]}'
  else
    req_body=$(cat "$req")
  fi

  read -ra modes <<< "$(get_storage_modes "$cfg")"

  for mode in "${modes[@]}"; do
    for rt in "${RUNTIMES[@]}"; do
      should_skip "$rt" && continue
      RUN_IDX=$((RUN_IDX + 1))
      port=$(next_port)
      info "[$RUN_IDX/$TOTAL_RUNS] $rt | $fixture | $mode on :$port"

      if ! start_server "$rt" "$port" "$cfg"; then continue; fi

      # Warmup
      hey -n 100 -c 5 -m POST -H "Content-Type: application/json" \
        -d "$req_body" -o csv "http://localhost:$port/execute" > /dev/null 2>&1

      # Benchmark — pipe directly to parse_hey, no temp file
      METRICS=$(hey -n "$NUM_REQUESTS" -c "$CONCURRENCY" -m POST \
        -H "Content-Type: application/json" \
        -d "$req_body" -o csv \
        "http://localhost:$port/execute" 2>/dev/null | parse_hey)
      IFS='|' read -r qps mean stddev p50 p90 p99 <<< "$METRICS"
      printf "  %-8s %-35s %7s %10s %10s %10s %10s %10s %10s\n" \
        "$rt" "$fixture" "$mode" "$qps" "$mean" "$stddev" "$p50" "$p90" "$p99" | tee -a "$REPORT"

      stop_server "$rt"
      sleep 0.2
    done
  done
done

echo >> "$REPORT"

info "Done. Report: $REPORT"
