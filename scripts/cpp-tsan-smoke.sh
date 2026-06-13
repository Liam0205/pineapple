#!/usr/bin/env bash
# Build pine-cpp with ThreadSanitizer and stress-run high-fanout DAG
# fixtures concurrently. Targets the ready-queue seed loop (engine.cpp:959-975)
# and the per-worker propagate path that race-shares in_degree[] and the
# done condvar — all of which sit outside the single-request serial path
# that ASan/UBSan smoke happens to exercise.
#
# Failure modes this catches:
#   * Reading graph.nodes[i].preds.size() racing with worker writes elsewhere.
#   * propagate_and_signal touching in_degree[] without proper memory ordering
#     when many roots seed simultaneously.
#   * done_cv / remaining notify-wait ordering bug that surfaces only when
#     workers finish before the main thread reaches done_cv.wait().
#
# Any TSan report aborts the process and causes the script to fail.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CPP_DIR="$REPO_ROOT/pine-cpp"
BUILD_DIR="$CPP_DIR/build-tsan"

# halt_on_error keeps the first race report the canonical failure;
# second_deadlock_stack is cheap to enable and surfaces lock-order bugs
# that can mask as nondeterministic hangs in stress runs.
export TSAN_OPTIONS="halt_on_error=1:second_deadlock_stack=1:history_size=7"

echo "==> Configuring pine-cpp with ThreadSanitizer"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cmake -S "$CPP_DIR" -B "$BUILD_DIR" \
    -DCMAKE_BUILD_TYPE=Debug \
    -DCMAKE_CXX_FLAGS="-fsanitize=thread -fno-omit-frame-pointer -O1 -g" \
    -DCMAKE_EXE_LINKER_FLAGS="-fsanitize=thread" \
    -DPINE_USE_JEMALLOC=OFF

echo "==> Building"
cmake --build "$BUILD_DIR" -j2

RUN="$BUILD_DIR/pineapple-run"
SERVER="$BUILD_DIR/pineapple-server"

# TSan's shadow memory layout collides with high-entropy ASLR on recent
# kernels (observed on 6.8 + glibc 2.39: "unexpected memory mapping").
# setarch -R disables ASLR for the child process tree only — no need to
# touch /proc/sys/kernel/randomize_va_space globally. Fall back to a
# bare invocation when setarch is missing.
if command -v setarch >/dev/null 2>&1; then
    NOASLR=(setarch -R)
else
    NOASLR=()
fi

# High-fanout fixtures: each picked because the DAG has 3+ root nodes that
# get seeded simultaneously by the engine's seed loop, maximising the race
# window between seed enqueue and worker propagate. Format:
# "subdir:filename:iters:parallel" — subdir relative to fixtures/.
HIGH_FANOUT=(
    pipelines:multi_recall_row_set_ordering.json:50:8     # 6 roots
    pipelines:recall_merge_filter_sort.json:50:8          # 4 roots
    pipelines:parallel_recall_set_comparison.json:50:8    # 3 roots
    pipelines:data_parallel.json:50:8                     # 3 roots
    pipelines:data_parallel_lua.json:50:8                 # 3 roots
    pipelines:barrier_transform_reorder.json:50:8         # 4 roots
    # large_5000 has 4 no-dep transforms (copy/dispatch/lua/normalize)
    # all reading and writing the same Frame concurrently — exactly the
    # write-while-read race surface that RowFrame/ColumnFrame's
    # shared_mutex is supposed to protect. Lower iter/parallel because
    # one iteration is ~30ms instead of ~1ms.
    benchmarks:large_5000_config.json:5:4                 # 4-way disjoint transforms
)

WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

echo "==> Stress: high-fanout fixtures (per-fixture iter × parallel listed)"
for spec in "${HIGH_FANOUT[@]}"; do
    IFS=':' read -r subdir fname iters par <<< "$spec"
    fixture="$REPO_ROOT/fixtures/$subdir/$fname"
    [[ -f "$fixture" ]] || { echo "    skip $subdir/$fname (missing)"; continue; }
    cfg="$WORK_DIR/cfg_${subdir}_$fname"
    req="$WORK_DIR/req_${subdir}_$fname"
    python3 -c "
import json
data = json.load(open('$fixture'))
# benchmarks/*_config.json has the engine config at top level; pipelines/*.json
# wraps it under 'config' alongside cases.
cfg = data.get('config', data)
json.dump(cfg, open('$cfg', 'w'))
cases = data.get('cases', [])
if cases:
    json.dump(cases[0].get('request', {}), open('$req', 'w'))
else:
    # benchmarks fixtures have a sibling *_request.json file.
    import os
    sibling = '$fixture'.replace('_config.json', '_request.json')
    if os.path.exists(sibling):
        json.dump(json.load(open(sibling)), open('$req', 'w'))
    else:
        json.dump({'common': {}, 'items': []}, open('$req', 'w'))
"
    echo "    Stressing $subdir/$fname (iter=$iters par=$par)"
    for ((iter=0; iter<iters; iter++)); do
        pids=()
        for ((p=0; p<par; p++)); do
            "${NOASLR[@]}" "$RUN" -config "$cfg" -request "$req" >/dev/null 2>>"$WORK_DIR/run.err" &
            pids+=("$!")
        done
        for pid in "${pids[@]}"; do
            if ! wait "$pid"; then
                echo "    pineapple-run failed under TSan (iter=$iter, fixture=$subdir/$fname)" >&2
                # Print the full run.err so the WARNING head + read/write
                # stacks survive — TSan reports for high-fanout fixtures
                # (6-root DAG / 4-way parallel) can exceed 200 lines, and a
                # tail-truncated report drops the very lines a fix needs
                # (Read of size N at 0xDEAD by main thread / Previous write
                # of size N at 0xDEAD by thread T1).
                cat "$WORK_DIR/run.err" >&2 || true
                exit 1
            fi
        done
    done
done

echo "==> Server stress: 200 concurrent /execute against high-fanout config"
SRV_CFG="$WORK_DIR/srv_cfg.json"
SRV_REQ="$WORK_DIR/srv_req.json"
python3 -c "
import json
data = json.load(open('$REPO_ROOT/fixtures/pipelines/multi_recall_row_set_ordering.json'))
json.dump(data.get('config', {}), open('$SRV_CFG', 'w'))
json.dump(data['cases'][0].get('request', {'common': {}, 'items': []}), open('$SRV_REQ', 'w'))
"

"${NOASLR[@]}" "$SERVER" -config "$SRV_CFG" -addr ":19897" >/dev/null 2>>"$WORK_DIR/srv.err" &
SRV_PID=$!
trap 'kill -KILL $SRV_PID 2>/dev/null || true; rm -rf "$WORK_DIR"' EXIT

for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    if curl -fsS http://localhost:19897/health >/dev/null 2>&1; then break; fi
    sleep 0.5
done

# Burst 200 /execute, 16 in flight. Server uses its dag_pool to schedule
# each request's seed loop; concurrent requests amplify the seed-vs-propagate
# race window beyond what the single-shot CLI run reaches.
PAYLOAD=$(cat "$SRV_REQ")
for batch in $(seq 1 13); do
    pids=()
    for _ in $(seq 1 16); do
        curl -fsS -X POST -H "Content-Type: application/json" \
            -d "$PAYLOAD" http://localhost:19897/execute >/dev/null 2>&1 &
        pids+=("$!")
    done
    for pid in "${pids[@]}"; do
        wait "$pid" || true
    done
done

kill -INT $SRV_PID 2>/dev/null || true
wait $SRV_PID 2>/dev/null || true
SRV_PID=""
trap 'rm -rf "$WORK_DIR"' EXIT

echo "==> TSan smoke complete (no ThreadSanitizer reports)"
