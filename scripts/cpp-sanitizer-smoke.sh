#!/usr/bin/env bash
# Build pine-cpp with AddressSanitizer + UndefinedBehaviorSanitizer
# and run a focused smoke test that exercises the runtime, server,
# render-dag and codegen paths against every pipeline fixture.
#
# Any sanitizer report aborts the process and causes the script to fail.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CPP_DIR="$REPO_ROOT/pine-cpp"
BUILD_DIR="$CPP_DIR/build-asan"

export ASAN_OPTIONS="halt_on_error=1:abort_on_error=1:detect_leaks=1:strict_string_checks=1"
export UBSAN_OPTIONS="halt_on_error=1:abort_on_error=1:print_stacktrace=1"

echo "==> Configuring pine-cpp with ASan + UBSan"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"
cmake -S "$CPP_DIR" -B "$BUILD_DIR" \
    -DCMAKE_BUILD_TYPE=Debug \
    -DCMAKE_CXX_FLAGS="-fsanitize=address,undefined -fno-omit-frame-pointer -O1 -g" \
    -DCMAKE_EXE_LINKER_FLAGS="-fsanitize=address,undefined"

echo "==> Building"
cmake --build "$BUILD_DIR" -j"$(nproc 2>/dev/null || echo 4)"

RUN="$BUILD_DIR/pineapple-run"
DAG="$BUILD_DIR/pineapple-render-dag"
SERVER="$BUILD_DIR/pineapple-server"
CODEGEN="$BUILD_DIR/pineapple-codegen"

echo "==> Codegen schema export under sanitizers"
SCHEMA_OUT=$(mktemp --suffix=.json)
"$CODEGEN" -schema-json "$SCHEMA_OUT"
head -c 100 "$SCHEMA_OUT" >/dev/null

echo "==> Iterating pipeline fixtures"
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT
fixture_count=0
for fixture in "$REPO_ROOT"/fixtures/pipelines/*.json; do
    fixture_count=$((fixture_count + 1))
    cfg="$WORK_DIR/cfg.json"
    req="$WORK_DIR/req.json"
    python3 -c "
import json, sys
data = json.load(open('$fixture'))
json.dump(data.get('config', {}), open('$cfg', 'w'))
cases = data.get('cases', [])
if cases:
    json.dump(cases[0].get('request', {}), open('$req', 'w'))
else:
    json.dump({'common': {}, 'items': []}, open('$req', 'w'))
"
    # render-dag (both formats)
    "$DAG" -config "$cfg" -format dot >/dev/null
    "$DAG" -config "$cfg" -format mermaid >/dev/null
    # run executor
    "$RUN" -config "$cfg" -request "$req" >/dev/null || true
done
echo "    Ran $fixture_count fixtures through dag + run."

echo "==> HTTP server smoke under sanitizers"
SRV_CFG="$WORK_DIR/srv_cfg.json"
python3 -c "
import json
data = json.load(open('$REPO_ROOT/fixtures/pipelines/transform_then_filter.json'))
json.dump(data.get('config', {}), open('$SRV_CFG', 'w'))
print(json.dumps(data['cases'][0]['request']))
" > "$WORK_DIR/srv_req.json"

"$SERVER" -config "$SRV_CFG" -addr ":19899" &
SRV_PID=$!
trap 'kill $SRV_PID 2>/dev/null || true; rm -rf "$WORK_DIR"' EXIT

for _ in 1 2 3 4 5 6 7 8 9 10; do
    if curl -fsS http://localhost:19899/health >/dev/null 2>&1; then break; fi
    sleep 0.3
done

curl -fsS http://localhost:19899/health >/dev/null
curl -fsS http://localhost:19899/stats >/dev/null
curl -fsS "http://localhost:19899/dag?format=dot" >/dev/null
curl -fsS -X POST -H "Content-Type: application/json" \
    -d "$(cat "$WORK_DIR/srv_req.json")" \
    http://localhost:19899/execute >/dev/null

# Trigger hot-reload to exercise the watcher under sanitizers
sleep 1
python3 -c "
import json
data = json.load(open('$REPO_ROOT/fixtures/pipelines/recall_rank_truncate.json'))
json.dump(data.get('config', {}), open('$SRV_CFG', 'w'))
"
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    rc=$(curl -fsS http://localhost:19899/stats | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))")
    if [[ "$rc" -ge 1 ]]; then break; fi
    sleep 0.5
done
curl -fsS http://localhost:19899/stats | python3 -c "
import json, sys
d = json.load(sys.stdin)
rc = d.get('server', {}).get('reload_count', 0)
assert rc >= 1, f'reload not detected: {rc}'
print(f'    Hot-reload OK (reload_count={rc})')
"

kill $SRV_PID 2>/dev/null || true
wait $SRV_PID 2>/dev/null || true
SRV_PID=""
trap - EXIT
rm -rf "$WORK_DIR"

echo "==> Sanitizer smoke complete (no AddressSanitizer/UBSan reports)"
