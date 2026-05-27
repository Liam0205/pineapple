#!/usr/bin/env bash
# Benchmark: per-node-thread (master) vs ready-queue-pool (current branch).
#
# Usage:
#   ./scripts/bench-dag-scheduler.sh [--generate-only]
#
# Prerequisites:
#   - hyperfine installed (brew install hyperfine)
#   - hey installed for throughput tests (go install github.com/rakyll/hey@latest)
#     or: brew install hey
#
# The script:
#   1. Generates synthetic DAG fixtures (5..500 nodes)
#   2. Builds both master and current-branch pineapple-run binaries
#   3. Runs single-request latency comparison (hyperfine)
#   4. Runs concurrent throughput comparison (server + hey)
#
# Output: results written to /tmp/bench_dag_scheduler/results.txt

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="/tmp/bench_dag_scheduler"
FIXTURE_DIR="$WORK_DIR/fixtures"
RESULTS="$WORK_DIR/results.txt"

mkdir -p "$WORK_DIR" "$FIXTURE_DIR"

# ─── Colors ───────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${CYAN}==> $*${NC}"; }
ok()    { echo -e "${GREEN}  ✓ $*${NC}"; }
err()   { echo -e "${RED}  ✗ $*${NC}" >&2; }

# ─── Step 1: Generate fixtures ────────────────────────────────────────
info "Generating synthetic DAG fixtures..."

python3 - "$FIXTURE_DIR" << 'PYEOF'
import json, math, os, sys

def gen_fixture(n_nodes, output_dir):
    width = max(2, int(math.sqrt(n_nodes)))
    operators = {}
    all_names = []

    operators["root_recall"] = {
        "type_name": "recall_static",
        "recall": True,
        "items": [{"item_id": f"item_{i}", "score": float(i * 10)} for i in range(10)],
        "$metadata": {
            "common_input": [], "common_output": [],
            "item_input": [], "item_output": ["item_id", "score"]
        }
    }
    all_names.append("root_recall")
    node_count = 1
    prev_layer_name = "root_recall"
    stage = 0

    while node_count < n_nodes:
        stage += 1
        stage_transforms = []
        actual_width = min(width, n_nodes - node_count - 1)
        if actual_width <= 0:
            actual_width = 1

        for i in range(actual_width):
            if node_count >= n_nodes:
                break
            name = f"tx_s{stage}_{i}"
            operators[name] = {
                "type_name": "transform_copy",
                "direction": "item_to_item",
                "src": "score", "dst": f"s{stage}_{i}",
                "sources": [prev_layer_name],
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": ["score"], "item_output": [f"s{stage}_{i}"]
                }
            }
            all_names.append(name)
            stage_transforms.append(name)
            node_count += 1

        if node_count >= n_nodes:
            break

        merge_name = f"mg_s{stage}"
        operators[merge_name] = {
            "type_name": "transform_copy",
            "direction": "item_to_item",
            "src": "score", "dst": f"mg{stage}",
            "sources": stage_transforms,
            "$metadata": {
                "common_input": [], "common_output": [],
                "item_input": ["score"], "item_output": [f"mg{stage}"]
            }
        }
        all_names.append(merge_name)
        prev_layer_name = merge_name
        node_count += 1

    config = {
        "_PINEAPPLE_VERSION": "0.9.1",
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": {"all_ops": {"pipeline": all_names}}
        },
        "pipeline_group": {"main": {"pipeline": ["all_ops"]}},
        "flow_contract": {
            "common_input": [], "common_output": [],
            "item_input": [], "item_output": ["item_id", "score"]
        }
    }
    request = {"common": {}, "items": []}

    with open(f"{output_dir}/bench_{n_nodes}_config.json", 'w') as f:
        json.dump(config, f)
    with open(f"{output_dir}/bench_{n_nodes}_req.json", 'w') as f:
        json.dump(request, f)
    print(f"    {n_nodes:>3} nodes ({len(operators)} ops, {stage} stages, width={width})")

out_dir = sys.argv[1]
for n in [5, 25, 50, 100, 200, 500]:
    gen_fixture(n, out_dir)
PYEOF

ok "Fixtures generated in $FIXTURE_DIR"

# ─── Step 2: Build binaries ───────────────────────────────────────────
info "Building binaries..."

CURRENT_BRANCH=$(git -C "$REPO_ROOT" rev-parse --abbrev-ref HEAD)
CURRENT_SHA=$(git -C "$REPO_ROOT" rev-parse --short HEAD)

BIN_NEW="$WORK_DIR/pineapple-run-new"
BIN_MASTER="$WORK_DIR/pineapple-run-master"
SRV_NEW="$WORK_DIR/pineapple-server-new"

# Build current branch
BUILD_DIR="$REPO_ROOT/pine-cpp/build"
mkdir -p "$BUILD_DIR"
(cd "$BUILD_DIR" && cmake .. -DCMAKE_BUILD_TYPE=Release -DCMAKE_CXX_FLAGS="-O2" >/dev/null 2>&1 \
  && make -j2 pineapple-run pineapple-server 2>&1 | tail -1)
cp "$BUILD_DIR/pineapple-run" "$BIN_NEW"
cp "$BUILD_DIR/pineapple-server" "$SRV_NEW"
ok "Built current branch ($CURRENT_BRANCH @ $CURRENT_SHA)"

# Build master version (checkout engine files only)
git -C "$REPO_ROOT" checkout master -- pine-cpp/src/runtime/engine.cpp pine-cpp/include/pine/pine.hpp 2>/dev/null
(cd "$BUILD_DIR" && make -j2 pineapple-run 2>&1 | tail -1)
cp "$BUILD_DIR/pineapple-run" "$BIN_MASTER"
# Restore current branch
git -C "$REPO_ROOT" checkout "$CURRENT_SHA" -- pine-cpp/src/runtime/engine.cpp pine-cpp/include/pine/pine.hpp 2>/dev/null
(cd "$BUILD_DIR" && make -j2 pineapple-run pineapple-server 2>&1 | tail -1)
ok "Built master baseline"

# Verify both work
"$BIN_NEW" -config "$FIXTURE_DIR/bench_5_config.json" -request "$FIXTURE_DIR/bench_5_req.json" >/dev/null 2>&1
"$BIN_MASTER" -config "$FIXTURE_DIR/bench_5_config.json" -request "$FIXTURE_DIR/bench_5_req.json" >/dev/null 2>&1
ok "Both binaries verified"

# ─── Step 3: Single-request latency ──────────────────────────────────
info "Running single-request latency benchmark..."

{
  echo "═══════════════════════════════════════════════════════════════"
  echo " DAG Scheduler Benchmark: per-node-thread vs ready-queue-pool"
  echo " Date: $(date -Iseconds)"
  echo " Branch: $CURRENT_BRANCH @ $CURRENT_SHA"
  echo " Machine: $(uname -n) ($(nproc) cores)"
  echo "═══════════════════════════════════════════════════════════════"
  echo
  echo "── Single-request latency (lower is better) ──"
  echo
} > "$RESULTS"

for n in 5 25 50 100 200 500; do
  echo "    $n nodes..."
  hyperfine --warmup 10 --min-runs 50 -N \
    -n "master" "$BIN_MASTER -config $FIXTURE_DIR/bench_${n}_config.json -request $FIXTURE_DIR/bench_${n}_req.json" \
    -n "new"    "$BIN_NEW -config $FIXTURE_DIR/bench_${n}_config.json -request $FIXTURE_DIR/bench_${n}_req.json" \
    --export-markdown "$WORK_DIR/latency_${n}.md" \
    2>&1 | grep -E "master|new|Summary|faster|slower" | tee -a "$RESULTS"
  echo >> "$RESULTS"
done

ok "Latency results written"

# ─── Step 4: Concurrent throughput ────────────────────────────────────
if command -v hey >/dev/null 2>&1; then
  info "Running concurrent throughput benchmark..."
  {
    echo
    echo "── Concurrent throughput (higher QPS is better) ──"
    echo "   50 concurrent connections, 2000 total requests"
    echo
  } >> "$RESULTS"

  for n in 5 50 200; do
    echo "    $n nodes..."

    # Start new server
    "$SRV_NEW" -config "$FIXTURE_DIR/bench_${n}_config.json" -addr ":18900" >/dev/null 2>&1 &
    SRV_PID=$!
    sleep 0.5

    # Wait for ready
    for _ in $(seq 1 20); do
      curl -s http://localhost:18900/health >/dev/null 2>&1 && break
      sleep 0.2
    done

    REQ_BODY=$(cat "$FIXTURE_DIR/bench_${n}_req.json")
    HEY_OUT=$(hey -n 2000 -c 50 -m POST \
      -H "Content-Type: application/json" \
      -d "$REQ_BODY" \
      http://localhost:18900/execute 2>&1)

    QPS=$(echo "$HEY_OUT" | grep "Requests/sec" | awk '{print $2}')
    P50=$(echo "$HEY_OUT" | grep "50%" | head -1 | awk '{print $2}')
    P99=$(echo "$HEY_OUT" | grep "99%" | head -1 | awk '{print $2}')

    printf "  [new]    %3d nodes: QPS=%-8s p50=%-8s p99=%s\n" "$n" "$QPS" "$P50" "$P99" | tee -a "$RESULTS"

    kill "$SRV_PID" 2>/dev/null; wait "$SRV_PID" 2>/dev/null || true
    sleep 0.3
  done

  ok "Throughput results written"
else
  echo "    (hey not installed — skipping throughput test. Install: brew install hey)" | tee -a "$RESULTS"
fi

# ─── Summary ──────────────────────────────────────────────────────────
echo
info "Results saved to: $RESULTS"
echo
cat "$RESULTS"
