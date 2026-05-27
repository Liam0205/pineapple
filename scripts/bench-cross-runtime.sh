#!/usr/bin/env bash
# Cross-runtime benchmark: pine-{go,java,python,cpp}
#
# Compares all four runtimes across multiple DAG sizes on:
#   1. Single-request latency (sequential, 1000 reqs per size)
#   2. Fixed-QPS latency under load (QPS=500, 15000 reqs, ~30s per size)
#   3. Max throughput (saturate with 50 concurrent connections)
#
# DAG sizes: 5, 15, 50, 100, 200, 500 nodes (observe_log no-ops)
#
# Prerequisites:
#   - hey (HTTP load generator): go install github.com/rakyll/hey@latest
#   - Go, Java 21, Python 3.13, cmake + build-essential + libluajit
#
# Usage:
#   ./scripts/bench-cross-runtime.sh [--skip go,python] [--sizes "5,50,200"]
#
# Output: /tmp/bench_cross_runtime/report.txt (+ raw hey output per phase)

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="/tmp/bench_cross_runtime"
FIXTURE_DIR="$WORK_DIR/fixtures"
REPORT="$WORK_DIR/report.txt"

SKIP_RUNTIMES=""
RUNTIMES=(go java python cpp)
DAG_SIZES=(5 15 50 100 200 500)

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip)  SKIP_RUNTIMES="$2"; shift 2 ;;
    --sizes) IFS=',' read -ra DAG_SIZES <<< "$2"; shift 2 ;;
    *)       echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

mkdir -p "$WORK_DIR" "$FIXTURE_DIR"

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

# ─── Generate synthetic DAG fixtures ──────────────────────────────────
info "Generating synthetic DAG fixtures (sizes: ${DAG_SIZES[*]})..."

python3 - "$FIXTURE_DIR" "${DAG_SIZES[@]}" << 'PYEOF'
import json, math, sys

def gen_fixture(n_nodes, output_dir):
    """Generate a diamond-shaped DAG of observe_log operators (true no-ops)."""
    width = max(2, int(math.sqrt(n_nodes)))
    operators = {}
    all_names = []

    operators["root_recall"] = {
        "type_name": "recall_static",
        "recall": True,
        "items": [{"item_id": f"item_{i}", "score": float(i)} for i in range(20)],
        "$metadata": {
            "common_input": [], "common_output": [],
            "item_input": [], "item_output": ["item_id", "score"]
        }
    }
    all_names.append("root_recall")
    node_count = 1
    prev_layer = ["root_recall"]
    stage = 0

    while node_count < n_nodes:
        stage += 1
        actual_width = min(width, n_nodes - node_count)
        if actual_width <= 0:
            break

        layer = []
        for i in range(actual_width):
            if node_count >= n_nodes:
                break
            name = f"obs_s{stage}_{i}"
            operators[name] = {
                "type_name": "observe_log",
                "sources": prev_layer,
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": ["item_id", "score"],
                    "item_output": ["item_id", "score"]
                }
            }
            all_names.append(name)
            layer.append(name)
            node_count += 1

        prev_layer = layer

    config = {
        "_PINEAPPLE_VERSION": "0.9.1",
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": {"bench": {"pipeline": all_names}}
        },
        "pipeline_group": {"main": {"pipeline": ["bench"]}},
        "flow_contract": {
            "common_input": [], "common_output": [],
            "item_input": [], "item_output": ["item_id", "score"]
        }
    }
    request = {"common": {}, "items": []}

    with open(f"{output_dir}/dag_{n_nodes}_config.json", 'w') as f:
        json.dump(config, f)
    with open(f"{output_dir}/dag_{n_nodes}_request.json", 'w') as f:
        json.dump(request, f)
    print(f"    {n_nodes:>3} nodes ({len(operators)} ops, {stage} stages)")

out_dir = sys.argv[1]
for n_str in sys.argv[2:]:
    gen_fixture(int(n_str), out_dir)
PYEOF

ok "Fixtures generated in $FIXTURE_DIR"

# ─── Build all runtimes ───────────────────────────────────────────────
info "Building runtimes..."

JAVA_CP=""

if ! should_skip go; then
  info "  Building Go..."
  (cd "$REPO_ROOT/pine-go" && go build -o "$WORK_DIR/server-go" ./cmd/pineapple-server/)
  ok "Go built"
fi

if ! should_skip java; then
  info "  Building Java..."
  (cd "$REPO_ROOT/pine-java" && mvn compile -B -q 2>/dev/null)
  JAVA_CP="$REPO_ROOT/pine-java/target/classes:$(cd "$REPO_ROOT/pine-java" && mvn dependency:build-classpath -B -q -Dmdep.outputFile=/dev/stdout 2>/dev/null | tail -1)"
  ok "Java built"
fi

if ! should_skip python; then
  info "  Installing Python deps..."
  pip install -q -e "$REPO_ROOT/pine-python/" 2>/dev/null || true
  ok "Python ready"
fi

if ! should_skip cpp; then
  info "  Building C++..."
  CPP_BUILD="$REPO_ROOT/pine-cpp/build"
  mkdir -p "$CPP_BUILD"
  (cd "$CPP_BUILD" && cmake .. -DCMAKE_BUILD_TYPE=Release >/dev/null 2>&1 \
    && cmake --build . -j2 --target pineapple-server 2>&1 | tail -1)
  cp "$CPP_BUILD/pineapple-server" "$WORK_DIR/server-cpp"
  ok "C++ built"
fi

# ─── Helper functions ─────────────────────────────────────────────────
BASE_PORT=19100
PORT_IDX=0

next_port() { PORT_IDX=$((PORT_IDX + 1)); echo $((BASE_PORT + PORT_IDX)); }

start_server() {
  local runtime="$1" port="$2" config="$3"
  local pid_file="$WORK_DIR/${runtime}.pid"

  case "$runtime" in
    java)
      java -cp "$JAVA_CP" -Dpine.config="$config" -Dpine.port="$port" \
        page.liam.pine.PineServer >"$WORK_DIR/${runtime}.log" 2>&1 &
      ;;
    go)
      "$WORK_DIR/server-go" -config "$config" -addr ":$port" >"$WORK_DIR/${runtime}.log" 2>&1 &
      ;;
    cpp)
      "$WORK_DIR/server-cpp" -config "$config" -addr ":$port" >"$WORK_DIR/${runtime}.log" 2>&1 &
      ;;
    python)
      python3 -m pine.cli.server -config "$config" -addr ":$port" >"$WORK_DIR/${runtime}.log" 2>&1 &
      ;;
  esac
  echo $! > "$pid_file"

  for _ in $(seq 1 40); do
    if curl -sf "http://localhost:$port/health" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  err "$runtime server failed to start on :$port (config: $config)"
  tail -20 "$WORK_DIR/${runtime}.log" >&2
  return 1
}

stop_server() {
  local runtime="$1"
  local pid_file="$WORK_DIR/${runtime}.pid"
  if [[ -f "$pid_file" ]]; then
    local pid
    pid=$(cat "$pid_file")
    kill -TERM "$pid" 2>/dev/null || true
    for _ in 1 2 3 4 5; do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.5
    done
    kill -KILL "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    rm -f "$pid_file"
  fi
}

cleanup() {
  for rt in "${RUNTIMES[@]}"; do
    stop_server "$rt"
  done
}
trap cleanup EXIT INT TERM

parse_hey() {
  local csv_file="$1"
  python3 -c "
import csv, math, sys
times, offsets = [], []
with open(sys.argv[1]) as f:
    for row in csv.DictReader(f):
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
" "$csv_file" 2>/dev/null || echo "N/A|N/A|N/A|N/A|N/A|N/A"
}

TABLE_HEADER="  %-8s %6s %10s %10s %10s %10s %10s %10s\n"
TABLE_SEP="  %-8s %6s %10s %10s %10s %10s %10s %10s\n"

print_table_header() {
  printf "$TABLE_HEADER" "Runtime" "Nodes" "QPS" "Mean" "Stddev" "P50" "P90" "P99"
  printf "$TABLE_SEP" "-------" "-----" "----------" "----------" "----------" "----------" "----------" "----------"
}

# ─── Report header ───────────────────────────────────────────────────
{
  echo "═══════════════════════════════════════════════════════════════════"
  echo " Cross-Runtime Benchmark: pine-{go,java,python,cpp}"
  echo " Date: $(date -Iseconds)"
  echo " DAG sizes: ${DAG_SIZES[*]}"
  echo " Machine: $(uname -n) ($(nproc) cores)"
  echo " Skipped: ${SKIP_RUNTIMES:-none}"
  echo "═══════════════════════════════════════════════════════════════════"
  echo
} > "$REPORT"

# PLACEHOLDER_PHASES

# ─── Phase 1: Single-request latency (sequential, 1000 reqs) ─────────
info "Phase 1: Single-request latency (1000 sequential requests per size)..."
{
  echo "── Phase 1: Single-request latency ──"
  echo "   1000 sequential requests (concurrency=1) per DAG size"
  echo
  print_table_header
} >> "$REPORT"

for n in "${DAG_SIZES[@]}"; do
  cfg="$FIXTURE_DIR/dag_${n}_config.json"
  req="$FIXTURE_DIR/dag_${n}_request.json"
  REQ_BODY=$(cat "$req")

  for rt in "${RUNTIMES[@]}"; do
    should_skip "$rt" && continue
    port=$(next_port)
    echo "    $rt / $n nodes on :$port..."

    if ! start_server "$rt" "$port" "$cfg"; then continue; fi

    HEY_OUT=$(hey -n 1000 -c 1 -m POST \
      -H "Content-Type: application/json" \
      -d "$REQ_BODY" \
      -o csv \
      "http://localhost:$port/execute" 2>&1)

    echo "$HEY_OUT" > "$WORK_DIR/phase1_${rt}_${n}.csv"
    METRICS=$(parse_hey "$WORK_DIR/phase1_${rt}_${n}.csv")
    IFS='|' read -r qps mean stddev p50 p90 p99 <<< "$METRICS"
    printf "  %-8s %6d %10s %10s %10s %10s %10s %10s\n" \
      "$rt" "$n" "$qps" "$mean" "$stddev" "$p50" "$p90" "$p99" | tee -a "$REPORT"

    stop_server "$rt"
    sleep 0.2
  done
done
echo >> "$REPORT"

# ─── Phase 2: Fixed QPS=500 latency (15000 reqs, ~30s) ───────────────
info "Phase 2: Fixed QPS=500 latency (15000 requests per size, ~30s each)..."
{
  echo "── Phase 2: Fixed QPS=500 latency ──"
  echo "   15000 requests at QPS=500 (~30s) per DAG size"
  echo
  print_table_header
} >> "$REPORT"

for n in "${DAG_SIZES[@]}"; do
  cfg="$FIXTURE_DIR/dag_${n}_config.json"
  req="$FIXTURE_DIR/dag_${n}_request.json"
  REQ_BODY=$(cat "$req")

  for rt in "${RUNTIMES[@]}"; do
    should_skip "$rt" && continue
    port=$(next_port)
    echo "    $rt / $n nodes on :$port..."

    if ! start_server "$rt" "$port" "$cfg"; then continue; fi

    HEY_OUT=$(hey -n 15000 -q 10 -c 50 -m POST \
      -H "Content-Type: application/json" \
      -d "$REQ_BODY" \
      -o csv \
      "http://localhost:$port/execute" 2>&1)

    echo "$HEY_OUT" > "$WORK_DIR/phase2_${rt}_${n}.csv"
    METRICS=$(parse_hey "$WORK_DIR/phase2_${rt}_${n}.csv")
    IFS='|' read -r qps mean stddev p50 p90 p99 <<< "$METRICS"
    printf "  %-8s %6d %10s %10s %10s %10s %10s %10s\n" \
      "$rt" "$n" "$qps" "$mean" "$stddev" "$p50" "$p90" "$p99" | tee -a "$REPORT"

    stop_server "$rt"
    sleep 0.2
  done
done
echo >> "$REPORT"

# ─── Phase 3: Max throughput (saturate) ───────────────────────────────
info "Phase 3: Max throughput (50 concurrent, 10000 requests per size)..."
{
  echo "── Phase 3: Max throughput ──"
  echo "   10000 requests, 50 concurrent connections per DAG size"
  echo
  print_table_header
} >> "$REPORT"

for n in "${DAG_SIZES[@]}"; do
  cfg="$FIXTURE_DIR/dag_${n}_config.json"
  req="$FIXTURE_DIR/dag_${n}_request.json"
  REQ_BODY=$(cat "$req")

  for rt in "${RUNTIMES[@]}"; do
    should_skip "$rt" && continue
    port=$(next_port)
    echo "    $rt / $n nodes on :$port..."

    if ! start_server "$rt" "$port" "$cfg"; then continue; fi

    HEY_OUT=$(hey -n 10000 -c 50 -m POST \
      -H "Content-Type: application/json" \
      -d "$REQ_BODY" \
      -o csv \
      "http://localhost:$port/execute" 2>&1)

    echo "$HEY_OUT" > "$WORK_DIR/phase3_${rt}_${n}.csv"
    METRICS=$(parse_hey "$WORK_DIR/phase3_${rt}_${n}.csv")
    IFS='|' read -r qps mean stddev p50 p90 p99 <<< "$METRICS"
    printf "  %-8s %6d %10s %10s %10s %10s %10s %10s\n" \
      "$rt" "$n" "$qps" "$mean" "$stddev" "$p50" "$p90" "$p99" | tee -a "$REPORT"

    stop_server "$rt"
    sleep 0.2
  done
done
echo >> "$REPORT"

# ─── Summary ──────────────────────────────────────────────────────────
{
  echo "Raw CSV data: $WORK_DIR/phase{1,2,3}_<runtime>_<nodes>.csv"
} >> "$REPORT"

info "Done. Report:"
echo
cat "$REPORT"
echo
info "Full report: $REPORT"
