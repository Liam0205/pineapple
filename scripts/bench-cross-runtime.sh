#!/usr/bin/env bash
# Cross-runtime benchmark: pine-{go,java,cpp}
#
# Dimensions:
#   - DAG size: 5, 50, 100, 500 nodes
#   - Storage mode: row, column
#   - Parallelism (DAG fan-out): 1, nproc/2, nproc, 2*nproc, 4*nproc
#   - Operator type: cpu, io, mixed
#
# Only runs max-throughput phase (50 concurrent, 10000 requests).
#
# Prerequisites:
#   - hey: go install github.com/rakyll/hey@latest
#   - Go, Java 21, cmake + build-essential + libluajit
#
# Usage:
#   ./scripts/bench-cross-runtime.sh [--skip go] [--sizes "5,50"] [--modes "row"]
#       [--parallelism "1,4,8"] [--ops "cpu,io"] [--requests 10000] [--concurrency 50]
#
# Output: /tmp/bench_cross_runtime/report.txt

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="/tmp/bench_cross_runtime"
FIXTURE_DIR="$WORK_DIR/fixtures"
REPORT="$WORK_DIR/report.txt"

NPROC=$(nproc)
SKIP_RUNTIMES=""
RUNTIMES=(go java cpp)
DAG_SIZES=(5 50 100 500)
STORAGE_MODES=(row column)
PARALLELISMS=(1 $((NPROC / 2)) $NPROC $((NPROC * 2)) $((NPROC * 4)))
OP_TYPES=(cpu io mixed)
NUM_REQUESTS=1000
CONCURRENCY=20

while [[ $# -gt 0 ]]; do
  case "$1" in
    --skip)        SKIP_RUNTIMES="$2"; shift 2 ;;
    --sizes)       IFS=',' read -ra DAG_SIZES <<< "$2"; shift 2 ;;
    --modes)       IFS=',' read -ra STORAGE_MODES <<< "$2"; shift 2 ;;
    --parallelism) IFS=',' read -ra PARALLELISMS <<< "$2"; shift 2 ;;
    --ops)         IFS=',' read -ra OP_TYPES <<< "$2"; shift 2 ;;
    --requests)    NUM_REQUESTS="$2"; shift 2 ;;
    --concurrency) CONCURRENCY="$2"; shift 2 ;;
    *)             echo "Unknown arg: $1" >&2; exit 1 ;;
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

# ─── Generate fixtures ────────────────────────────────────────────────
info "Generating fixtures..."
info "  Sizes: ${DAG_SIZES[*]}"
info "  Storage: ${STORAGE_MODES[*]}"
info "  Parallelism: ${PARALLELISMS[*]}"
info "  Op types: ${OP_TYPES[*]}"

python3 - "$FIXTURE_DIR" "${DAG_SIZES[*]}" "${STORAGE_MODES[*]}" "${PARALLELISMS[*]}" "${OP_TYPES[*]}" << 'PYEOF'
import json, sys

def gen_fixture(n_nodes, storage_mode, fan_out, op_type, output_dir):
    """Generate a DAG with controlled fan-out width and operator type."""
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

    def make_op(name, op_type, stage_idx, node_idx):
        """Create operator config based on type."""
        if op_type == "cpu":
            return {
                "type_name": "transform_bench_cpu",
                "sources": prev_layer,
                "iterations": 100,
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": ["item_id", "score"],
                    "item_output": ["item_id", "score", "_bench_result"]
                }
            }
        elif op_type == "io":
            return {
                "type_name": "transform_bench_sleep",
                "sources": prev_layer,
                "delay_ms": 5,
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": ["item_id", "score"],
                    "item_output": ["item_id", "score", "_bench_slept"]
                }
            }
        else:  # mixed: alternate cpu and io
            if (stage_idx + node_idx) % 2 == 0:
                return {
                    "type_name": "transform_bench_cpu",
                    "sources": prev_layer,
                    "iterations": 100,
                    "$metadata": {
                        "common_input": [], "common_output": [],
                        "item_input": ["item_id", "score"],
                        "item_output": ["item_id", "score", "_bench_result"]
                    }
                }
            else:
                return {
                    "type_name": "transform_bench_sleep",
                    "sources": prev_layer,
                    "delay_ms": 5,
                    "$metadata": {
                        "common_input": [], "common_output": [],
                        "item_input": ["item_id", "score"],
                        "item_output": ["item_id", "score", "_bench_slept"]
                    }
                }

    while node_count < n_nodes:
        stage += 1
        width = min(fan_out, n_nodes - node_count)
        if width <= 0:
            break
        layer = []
        for i in range(width):
            if node_count >= n_nodes:
                break
            name = f"op_s{stage}_{i}"
            operators[name] = make_op(name, op_type, stage, i)
            all_names.append(name)
            layer.append(name)
            node_count += 1
        prev_layer = layer

    config = {
        "_PINEAPPLE_VERSION": "0.9.2",
        "storage_mode": storage_mode,
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

    tag = f"n{n_nodes}_s{storage_mode}_p{fan_out}_{op_type}"
    with open(f"{output_dir}/{tag}_config.json", 'w') as f:
        json.dump(config, f)
    with open(f"{output_dir}/{tag}_request.json", 'w') as f:
        json.dump(request, f)

out_dir = sys.argv[1]
sizes = [int(x) for x in sys.argv[2].split()]
modes = sys.argv[3].split()
pars = [int(x) for x in sys.argv[4].split()]
ops = sys.argv[5].split()

count = 0
for n in sizes:
    for mode in modes:
        for p in pars:
            for op in ops:
                gen_fixture(n, mode, p, op, out_dir)
                count += 1
print(f"  Generated {count} fixture pairs")
PYEOF

ok "Fixtures generated"

# ─── Build runtimes ───────────────────────────────────────────────────
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

if ! should_skip cpp; then
  info "  Building C++..."
  CPP_BUILD="$REPO_ROOT/pine-cpp/build"
  mkdir -p "$CPP_BUILD"
  (cd "$CPP_BUILD" && cmake .. -DCMAKE_BUILD_TYPE=Release -DCMAKE_POLICY_VERSION_MINIMUM=3.5 >/dev/null 2>&1 \
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
  case "$runtime" in
    java) java -cp "$JAVA_CP" -Dpine.config="$config" -Dpine.port="$port" \
      page.liam.pine.PineServer >"$WORK_DIR/${runtime}.log" 2>&1 & ;;
    go) "$WORK_DIR/server-go" -config "$config" -addr ":$port" >"$WORK_DIR/${runtime}.log" 2>&1 & ;;
    cpp) "$WORK_DIR/server-cpp" -config "$config" -addr ":$port" >"$WORK_DIR/${runtime}.log" 2>&1 & ;;
  esac
  echo $! > "$pid_file"
  for _ in $(seq 1 40); do
    curl -sf "http://localhost:$port/health" >/dev/null 2>&1 && return 0
    sleep 0.25
  done
  err "$runtime server failed to start on :$port"
  tail -20 "$WORK_DIR/${runtime}.log" >&2
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

cleanup() { for rt in "${RUNTIMES[@]}"; do stop_server "$rt"; done; }
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

# ─── Report header ────────────────────────────────────────────────────
{
  echo "═══════════════════════════════════════════════════════════════════"
  echo " Cross-Runtime Benchmark: pine-{go,java,cpp}"
  echo " Date: $(date -Iseconds)"
  echo " Machine: $(uname -n) (${NPROC} cores)"
  echo " Dimensions: sizes=${DAG_SIZES[*]} storage=${STORAGE_MODES[*]}"
  echo "   parallelism=${PARALLELISMS[*]} ops=${OP_TYPES[*]}"
  echo " Load: ${NUM_REQUESTS} requests, ${CONCURRENCY} concurrent"
  echo " Skipped: ${SKIP_RUNTIMES:-none}"
  echo "═══════════════════════════════════════════════════════════════════"
  echo
} > "$REPORT"

# ─── Benchmark loop ──────────────────────────────────────────────────
TOTAL_COMBOS=$(( ${#DAG_SIZES[@]} * ${#STORAGE_MODES[@]} * ${#PARALLELISMS[@]} * ${#OP_TYPES[@]} * ${#RUNTIMES[@]} ))
COMBO_IDX=0

TABLE_HEADER="  %-8s %5s %7s %4s %6s %10s %10s %10s %10s %10s %10s\n"

{
  printf "$TABLE_HEADER" "Runtime" "Nodes" "Storage" "Par" "OpType" "QPS" "Mean" "Stddev" "P50" "P90" "P99"
  printf "$TABLE_HEADER" "-------" "-----" "-------" "---" "------" "----------" "----------" "----------" "----------" "----------" "----------"
} >> "$REPORT"

for n in "${DAG_SIZES[@]}"; do
  for mode in "${STORAGE_MODES[@]}"; do
    for par in "${PARALLELISMS[@]}"; do
      for op in "${OP_TYPES[@]}"; do
        tag="n${n}_s${mode}_p${par}_${op}"
        cfg="$FIXTURE_DIR/${tag}_config.json"
        req="$FIXTURE_DIR/${tag}_request.json"
        [[ -f "$cfg" ]] || continue
        REQ_BODY=$(cat "$req")

        for rt in "${RUNTIMES[@]}"; do
          should_skip "$rt" && continue
          COMBO_IDX=$((COMBO_IDX + 1))
          port=$(next_port)
          echo "  [$COMBO_IDX/$TOTAL_COMBOS] $rt $tag on :$port..."

          if ! start_server "$rt" "$port" "$cfg"; then continue; fi

          # Warmup
          hey -n 200 -c 10 -m POST -H "Content-Type: application/json" \
            -d "$REQ_BODY" -o csv "http://localhost:$port/execute" > /dev/null 2>&1

          # Benchmark
          hey -n "$NUM_REQUESTS" -c "$CONCURRENCY" -m POST \
            -H "Content-Type: application/json" \
            -d "$REQ_BODY" -o csv \
            "http://localhost:$port/execute" > "$WORK_DIR/${tag}_${rt}.csv" 2>&1

          METRICS=$(parse_hey "$WORK_DIR/${tag}_${rt}.csv")
          IFS='|' read -r qps mean stddev p50 p90 p99 <<< "$METRICS"
          printf "  %-8s %5d %7s %4d %6s %10s %10s %10s %10s %10s %10s\n" \
            "$rt" "$n" "$mode" "$par" "$op" "$qps" "$mean" "$stddev" "$p50" "$p90" "$p99" | tee -a "$REPORT"

          stop_server "$rt"
          sleep 0.2
        done
      done
    done
  done
done

echo >> "$REPORT"
{
  echo "Raw CSV data: $WORK_DIR/<tag>_<runtime>.csv"
} >> "$REPORT"

info "Done. Report:"
echo
cat "$REPORT"
echo
info "Full report: $REPORT"
