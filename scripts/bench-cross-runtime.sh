#!/usr/bin/env bash
# Cross-runtime benchmark: pine-{go,java,cpp}
#
# Fixture-driven: loads all *_config.json from fixtures/benchmarks/ by default.
# Each fixture is self-describing (DAG topology, operator mix, storage mode).
#
# Prerequisites:
#   - hey: go install github.com/rakyll/hey@latest
#   - Go, Java 25, cmake + build-essential + libluajit
#
# Usage:
#   ./scripts/bench-cross-runtime.sh [--skip go] [--modes "row,column"]
#       [--requests 1000] [--concurrency 20] [--generate] [--filter "realistic"]
#       [--no-resource-limit] [--cpu-list 0,1] [--mem-max 4G]
#
# Options:
#   --skip               Runtimes to skip (comma-separated)
#   --modes              Run every fixture once per storage_mode (comma-separated),
#                        overriding whatever the fixture config declares. Each
#                        run gets a config copy with the field rewritten, so
#                        "row,column" is a genuine A/B on the same shape.
#   --requests           Number of requests per benchmark run (default: 1000)
#   --concurrency        Concurrent connections (default: 20)
#   --generate           Also generate synthetic fixtures via bench-generate-fixtures.py
#   --filter             Only run fixtures whose name matches this substring
#   --no-resource-limit  Disable server-side cgroup resource limit (default: ON, 2C/4G)
#   --cpu-list           Server CPU affinity list passed to taskset -c (default: 0,1)
#   --mem-max            Server memory cap (systemd MemoryMax, default: 4G; swap forced 0)
#
# Resource limit applies to the SERVER process only; the hey client is unrestricted
# so it does not steal CPU from the runtime under test. Override via env:
#   BENCH_RESOURCE_LIMIT=0 BENCH_CPU_LIST=0,1,2,3 BENCH_MEM_MAX=8G ./...
#
# Output: bench-results/report-<timestamp>.txt (in repo root, not /tmp), plus
# bench-results/report.txt as a stable alias to the run just finished — that is
# the name CI consumes.

set -euo pipefail
# Run in its own process group so cleanup can kill the whole group
if [[ "${BENCH_IN_PGRP:-}" != "1" ]]; then
  BENCH_IN_PGRP=1 exec setsid bash "$0" "$@"
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="/tmp/bench_cross_runtime"
RESULTS_DIR="$REPO_ROOT/bench-results"
REPORT="$RESULTS_DIR/report-$(date +%Y%m%d-%H%M%S).txt"
# Stable alias to the run just finished. The timestamped file is the archive
# (local runs accumulate a history); this fixed name is the machine-readable
# entry point so CI and tooling never have to guess a timestamp. Without it
# the nightly workflow globbed a hardcoded /tmp path that stopped matching
# when reports moved to the repo root, and uploaded empty artifacts for
# months while the job stayed green.
REPORT_LATEST="$RESULTS_DIR/report.txt"
FIXTURE_SRC="$REPO_ROOT/fixtures/benchmarks"

NPROC=$(nproc)
SKIP_RUNTIMES=""
RUNTIMES=(go java cpp)
STORAGE_MODES=()
NUM_REQUESTS=1000
CONCURRENCY=20
GENERATE=false
FILTER=""
RESOURCE_LIMIT="${BENCH_RESOURCE_LIMIT:-1}"
CPU_LIST="${BENCH_CPU_LIST:-0,1}"
MEM_MAX="${BENCH_MEM_MAX:-4G}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip)              SKIP_RUNTIMES="$2"; shift 2 ;;
    --modes)             IFS=',' read -ra STORAGE_MODES <<< "$2"; shift 2 ;;
    --requests)          NUM_REQUESTS="$2"; shift 2 ;;
    --concurrency)       CONCURRENCY="$2"; shift 2 ;;
    --generate)          GENERATE=true; shift ;;
    --filter)            FILTER="$2"; shift 2 ;;
    --no-resource-limit) RESOURCE_LIMIT=0; shift ;;
    --cpu-list)          CPU_LIST="$2"; shift 2 ;;
    --mem-max)           MEM_MAX="$2"; shift 2 ;;
    *)                   echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

# When RESOURCE_LIMIT=1 (default), each server is launched inside a transient
# user-scope cgroup with `MemoryMax=$MEM_MAX` (no swap) and `taskset -c $CPU_LIST`,
# so the runtime under test sees a uniform constrained env and the hey client
# (which is unrestricted) cannot starve it. Toggle off via --no-resource-limit
# when running on a beefy CI box or for absolute peak.
if [[ "$RESOURCE_LIMIT" == "1" ]]; then
  if ! command -v systemd-run >/dev/null 2>&1; then
    echo "Error: systemd-run not found; pass --no-resource-limit to disable cgroup limits" >&2
    exit 1
  fi
  if ! command -v taskset >/dev/null 2>&1; then
    echo "Error: taskset not found; pass --no-resource-limit to disable cgroup limits" >&2
    exit 1
  fi
fi

mkdir -p "$WORK_DIR" "$RESULTS_DIR"
# Clean up any leftover artifacts from a previous run
rm -f "$WORK_DIR"/*.csv "$WORK_DIR"/*.log "$WORK_DIR"/*.pid
rm -f "$WORK_DIR"/server-go "$WORK_DIR"/server-cpp
rm -f "$WORK_DIR"/*.row.json "$WORK_DIR"/*.column.json

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
  local unit_file="$WORK_DIR/${runtime}.unit"
  local sink="/dev/null"
  # Set BENCH_VERBOSE=1 to capture server logs for debugging startup failures
  [[ "${BENCH_VERBOSE:-}" == "1" ]] && sink="$WORK_DIR/${runtime}.log"
  local -a cmd=()
  # JAVA_BENCH_OPTS lets the caller inject JVM flags (e.g. `-XX:+UseZGC`
  # for generational ZGC, default since JDK 24) for the java leg without
  # touching the script. Word-split via $JAVA_BENCH_OPTS expansion; empty
  # default = no flags.
  local -a java_opts=()
  if [[ -n "${JAVA_BENCH_OPTS:-}" ]]; then
    # shellcheck disable=SC2206  # intentional word-splitting for env-supplied flags
    java_opts=(${JAVA_BENCH_OPTS})
  fi
  case "$runtime" in
    java) cmd=(java "${java_opts[@]}" -cp "$JAVA_CP" -Dpine.bench=true -Dpine.config="$config" -Dpine.port="$port"
              page.liam.pine.PineServer) ;;
    go)   cmd=("$WORK_DIR/server-go" -config "$config" -addr ":$port") ;;
    cpp)  if [[ -n "${CPP_LD_PRELOAD:-}" ]]; then
            cmd=(env "LD_PRELOAD=$CPP_LD_PRELOAD" "$WORK_DIR/server-cpp" -config "$config" -addr ":$port")
          else
            cmd=("$WORK_DIR/server-cpp" -config "$config" -addr ":$port")
          fi ;;
  esac
  rm -f "$unit_file"
  if [[ "$RESOURCE_LIMIT" == "1" ]]; then
    # Each server gets a unique transient scope so cleanup is deterministic.
    local unit="pine-bench-${runtime}-${port}-$$.scope"
    systemd-run --user --scope --quiet --collect --unit="$unit" \
      -p "MemoryMax=$MEM_MAX" -p MemorySwapMax=0 \
      taskset -c "$CPU_LIST" "${cmd[@]}" >"$sink" 2>&1 &
    echo $! > "$pid_file"
    echo "$unit" > "$unit_file"
  else
    "${cmd[@]}" >"$sink" 2>&1 &
    echo $! > "$pid_file"
  fi
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
  local unit_file="$WORK_DIR/${runtime}.unit"
  if [[ -f "$unit_file" ]]; then
    local unit; unit=$(cat "$unit_file")
    systemctl --user stop "$unit" >/dev/null 2>&1 || true
    rm -f "$unit_file"
  fi
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
  # storage-mode-pinned config copies written by config_for_mode
  rm -f "$WORK_DIR"/*.row.json "$WORK_DIR"/*.column.json
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

# ─── Materialize a config pinned to one storage mode ─────────────────
# storage_mode is a root-level config field, not a server flag — none of the
# runtimes accept it on the command line. So honouring --modes means writing
# a copy of the config with the field rewritten and pointing the server at
# that. Runtime-agnostic, and the same trick benchStorageAB uses in
# pine-go/benchmarks/bench_storage_ab_test.go (cfg["storage_mode"] = mode).
#
# Before this existed, $mode reached only the report column: --modes
# "row,column" ran the same config twice and labelled the two identical runs
# differently, so the column row of every such report was really row-mode
# numbers.
#
# Echoes the path to use. When the fixture already declares the mode we want
# there is nothing to rewrite, so the original is returned untouched.
config_for_mode() {
  local config_file="$1" mode="$2"
  local declared
  # No `|| echo "row"` fallback here: that would fold "parse failed" into
  # "declares row", and an unreadable config whose target mode happened to be
  # row would then be handed to the server as if nothing were wrong. Probe
  # failure must reach the caller as a skip, same as rewrite failure.
  declared=$(python3 -c "import json,sys; c=json.load(open(sys.argv[1])); print(c.get('storage_mode','row'))" \
    "$config_file" 2>/dev/null) || return 0
  if [[ "$declared" == "$mode" ]]; then
    echo "$config_file"
    return
  fi
  local out="$WORK_DIR/$(basename "$config_file" .json).$mode.json"
  # On failure emit nothing: the caller treats an unreadable path as "skip
  # this run". Falling back to the original config would silently reintroduce
  # exactly the mislabelled-numbers bug this function exists to fix.
  python3 -c "
import json, sys
cfg = json.load(open(sys.argv[1]))
cfg['storage_mode'] = sys.argv[3]
json.dump(cfg, open(sys.argv[2], 'w'))
" "$config_file" "$out" "$mode" >&2 || return 0
  echo "$out"
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
  if [[ "$RESOURCE_LIMIT" == "1" ]]; then
    echo " Server limit: taskset -c $CPU_LIST  MemoryMax=$MEM_MAX  MemorySwapMax=0  (cgroup-isolated)"
  else
    echo " Server limit: (none — full host)"
  fi
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
COMPLETED=0
SKIPPED=()
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

      mode_cfg=$(config_for_mode "$cfg" "$mode")
      if [[ -z "$mode_cfg" ]]; then
        err "skipping $rt | $fixture | $mode (could not pin storage_mode)"
        SKIPPED+=("$rt|$fixture|$mode: could not pin storage_mode")
        continue
      fi

      if ! start_server "$rt" "$port" "$mode_cfg"; then
        SKIPPED+=("$rt|$fixture|$mode: server failed to start")
        continue
      fi

      # Warmup
      hey -n 100 -c 5 -m POST -H "Content-Type: application/json" \
        -d "$req_body" -o csv "http://localhost:$port/execute" > /dev/null 2>&1

      # Benchmark — pipe directly to parse_hey, no temp file.
      # `|| true`: hey exits 0 on connection failures but 1 on argument errors
      # (e.g. -c greater than -n). Under set -e that would abort the whole run
      # right here, before the shortfall warning and the report.txt copy — the
      # script would die quietly having written a partial report and no stable
      # alias. parse_hey already yields an all-N/A row for that case, which is
      # now counted as a skip.
      METRICS=$(hey -n "$NUM_REQUESTS" -c "$CONCURRENCY" -m POST \
        -H "Content-Type: application/json" \
        -d "$req_body" -o csv \
        "http://localhost:$port/execute" 2>/dev/null | parse_hey || true)
      IFS='|' read -r qps mean stddev p50 p90 p99 <<< "$METRICS"
      printf "  %-8s %-35s %7s %10s %10s %10s %10s %10s %10s\n" \
        "$rt" "$fixture" "$mode" "$qps" "$mean" "$stddev" "$p50" "$p90" "$p99" | tee -a "$REPORT"
      # An all-N/A row is not a completed run: parse_hey emits it when hey
      # collected no samples at all, and both report parsers discard the row.
      # Counting it would make COMPLETED == TOTAL_RUNS and silence the
      # shortfall warning in the most likely failure mode of all — a server
      # that answers /health and then dies under load. hey exits 0 when it
      # cannot connect, so neither set -e nor pipefail catches this.
      if [[ "$qps" == "N/A" ]]; then
        err "no samples for $rt | $fixture | $mode"
        SKIPPED+=("$rt|$fixture|$mode: benchmark produced no samples")
      else
        COMPLETED=$((COMPLETED + 1))
      fi

      stop_server "$rt"
      sleep 0.2
    done
  done
done

echo >> "$REPORT"

# Missing rows must announce themselves. A short report is otherwise
# indistinguishable from a healthy one — the reader would have to count rows
# by hand — and that is the same failure shape as the empty-artifact bug: the
# job stays green while the data silently thins out. The Bark notification
# only forwards the analysis Verdict section, so a warning that lives solely
# in stderr never leaves CI.
if (( COMPLETED != TOTAL_RUNS )); then
  {
    echo "WARNING: only $COMPLETED of $TOTAL_RUNS planned runs produced data."
    for s in "${SKIPPED[@]}"; do echo "  skipped: $s"; done
  } | tee -a "$REPORT" >&2
fi

# Copy rather than symlink: CI artifact upload follows the path but archives
# the link target's name, and downstream `gh run download` then lands a
# timestamped file the consumer cannot predict.
cp "$REPORT" "$REPORT_LATEST"

info "Done. Report: $REPORT (also: $REPORT_LATEST)"
