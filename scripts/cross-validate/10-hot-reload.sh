#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 10. Server hot-reload parity ----------
echo
echo "==> [10/$TOTAL_SECTIONS] Server hot-reload parity"

reload_pass=0
reload_total=0

# Start with one config, then swap to another
RELOAD_CONFIG_A="$WORK_DIR/reload_config.json"
RELOAD_CONFIG_B="$WORK_DIR/reload_config_b.json"

# Config A: simple transform_then_filter (2 operators)
python3 -c "
import json
with open('$REPO_ROOT/fixtures/pipelines/transform_then_filter.json') as f:
    data = json.load(f)
with open('$RELOAD_CONFIG_A', 'w') as cf:
    json.dump(data.get('config', {}), cf)
"

# Config B: recall_rank_truncate (different operators)
python3 -c "
import json
with open('$REPO_ROOT/fixtures/pipelines/recall_rank_truncate.json') as f:
    data = json.load(f)
with open('$RELOAD_CONFIG_B', 'w') as cf:
    json.dump(data.get('config', {}), cf)
"

RELOAD_GO_PORT=18930
RELOAD_JAVA_PORT=18931

"$WORK_DIR/pineapple-server" -config "$RELOAD_CONFIG_A" -addr ":$RELOAD_GO_PORT" &
GO_SRV_PID=$!
java -cp "$JAVA_CP" -Dpine.config="$RELOAD_CONFIG_A" -Dpine.port=$RELOAD_JAVA_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

if srv_ready $RELOAD_GO_PORT && srv_ready $RELOAD_JAVA_PORT; then
  # Execute one request on each server to populate operator stats
  RELOAD_REQ=$(python3 -c "
import json
with open('$REPO_ROOT/fixtures/pipelines/transform_then_filter.json') as f:
    data = json.load(f)
print(json.dumps(data['cases'][0]['request']))
")
  curl -s -X POST -H "Content-Type: application/json" -d "$RELOAD_REQ" "http://localhost:$RELOAD_GO_PORT/execute" >/dev/null 2>&1 || true
  curl -s -X POST -H "Content-Type: application/json" -d "$RELOAD_REQ" "http://localhost:$RELOAD_JAVA_PORT/execute" >/dev/null 2>&1 || true

  # Test 1: Initial operator count matches (after one execution)
  reload_total=$((reload_total + 1))
  go_ops_before=$(curl -s "http://localhost:$RELOAD_GO_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
  java_ops_before=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
  if [[ "$go_ops_before" == "$java_ops_before" && "$go_ops_before" != "0" ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [1] Initial operator count matches ($go_ops_before operators)"
  else
    fail "hot-reload: initial operator count mismatch (Go=$go_ops_before, Java=$java_ops_before)"
  fi

  # Swap config file contents (simulates hot-reload trigger)
  cp "$RELOAD_CONFIG_B" "$RELOAD_CONFIG_A"
  sleep 3  # Wait for mtime poll to detect change

  # Test 2: After reload, DAG structure should change (use /dag endpoint)
  reload_total=$((reload_total + 1))
  go_dag_before=$("$WORK_DIR/pineapple-dag" -config "$WORK_DIR/reload_config_b.json" -format dot 2>/dev/null || echo "")
  go_dag_after=$(curl -s "http://localhost:$RELOAD_GO_PORT/dag?format=dot")
  java_dag_after=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/dag?format=dot")
  if [[ "$go_dag_after" == "$java_dag_after" && -n "$go_dag_after" ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [2] After reload, DAG matches between Go and Java"
  else
    fail "hot-reload: DAG mismatch after reload"
  fi

  # Test 3: reload_count in server stats incremented
  reload_total=$((reload_total + 1))
  go_reload_count=$(curl -s "http://localhost:$RELOAD_GO_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))")
  java_reload_count=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))")
  if [[ "$go_reload_count" -ge 1 && "$java_reload_count" -ge 1 ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [3] reload_count >= 1 (Go=$go_reload_count, Java=$java_reload_count)"
  else
    fail "hot-reload: reload_count not incremented (Go=$go_reload_count, Java=$java_reload_count)"
  fi

  kill $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
else
  fail "hot-reload: servers failed to start"
  kill $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
fi

if [[ $reload_total -gt 0 && $reload_pass -eq $reload_total ]]; then
  pass "hot-reload parity ($reload_pass/$reload_total checks)"
elif [[ $reload_total -eq 0 ]]; then
  pass "hot-reload parity (skipped)"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
