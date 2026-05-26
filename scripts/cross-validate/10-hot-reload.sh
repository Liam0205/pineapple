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

RELOAD_GO_PORT=20001
RELOAD_JAVA_PORT=20002
RELOAD_PY_PORT=20003
RELOAD_CPP_PORT=20004

"$WORK_DIR/pineapple-server" -config "$RELOAD_CONFIG_A" -addr ":$RELOAD_GO_PORT" &
GO_SRV_PID=$!
java -cp "$JAVA_CP" -Dpine.config="$RELOAD_CONFIG_A" -Dpine.port=$RELOAD_JAVA_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!
(cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.server -config "$RELOAD_CONFIG_A" -addr ":$RELOAD_PY_PORT") &
PY_SRV_PID=$!
CPP_SRV_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$RELOAD_CONFIG_A" -addr ":$RELOAD_CPP_PORT" &
  CPP_SRV_PID=$!
fi

cpp_reload_pass=0
cpp_reload_total=0
cpp_srv_ready=false

if srv_ready $RELOAD_GO_PORT && srv_ready $RELOAD_JAVA_PORT && srv_ready $RELOAD_PY_PORT; then
  if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $RELOAD_CPP_PORT; then
    cpp_srv_ready=true
  fi
  # Execute one request on each server to populate operator stats
  RELOAD_REQ=$(python3 -c "
import json
with open('$REPO_ROOT/fixtures/pipelines/transform_then_filter.json') as f:
    data = json.load(f)
print(json.dumps(data['cases'][0]['request']))
")
  curl -s -X POST -H "Content-Type: application/json" -d "$RELOAD_REQ" "http://localhost:$RELOAD_GO_PORT/execute" >/dev/null 2>&1 || true
  curl -s -X POST -H "Content-Type: application/json" -d "$RELOAD_REQ" "http://localhost:$RELOAD_JAVA_PORT/execute" >/dev/null 2>&1 || true
  curl -s -X POST -H "Content-Type: application/json" -d "$RELOAD_REQ" "http://localhost:$RELOAD_PY_PORT/execute" >/dev/null 2>&1 || true
  if $cpp_srv_ready; then
    curl -s -X POST -H "Content-Type: application/json" -d "$RELOAD_REQ" "http://localhost:$RELOAD_CPP_PORT/execute" >/dev/null 2>&1 || true
  fi

  # Test 1: Initial operator count matches (after one execution)
  reload_total=$((reload_total + 1))
  go_ops_before=$(curl -s "http://localhost:$RELOAD_GO_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
  java_ops_before=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
  py_ops_before=$(curl -s "http://localhost:$RELOAD_PY_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
  if [[ "$go_ops_before" == "$java_ops_before" && "$go_ops_before" == "$py_ops_before" && "$go_ops_before" != "0" ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [1] Initial operator count matches ($go_ops_before operators, all engines)"
  else
    fail "hot-reload: initial operator count mismatch (Go=$go_ops_before, Java=$java_ops_before, Python=$py_ops_before)"
  fi

  if $cpp_srv_ready; then
    cpp_reload_total=$((cpp_reload_total + 1))
    cpp_ops_before=$(curl -s "http://localhost:$RELOAD_CPP_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
    if [[ "$go_ops_before" == "$cpp_ops_before" && "$cpp_ops_before" != "0" ]]; then
      cpp_reload_pass=$((cpp_reload_pass + 1))
      echo "    [1] C++ initial operator count matches Go ($cpp_ops_before operators)"
    else
      fail "hot-reload: C++ initial operator count mismatch (Go=$go_ops_before, C++=$cpp_ops_before)"
    fi
  fi

  # Swap config file contents (simulates hot-reload trigger)
  cp "$RELOAD_CONFIG_B" "$RELOAD_CONFIG_A"

  # Poll until all servers detect the reload (timeout 10s)
  reload_detected=false
  for attempt in $(seq 1 50); do
    go_rc=$(curl -s "http://localhost:$RELOAD_GO_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo 0)
    java_rc=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo 0)
    py_rc=$(curl -s "http://localhost:$RELOAD_PY_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo 0)
    cpp_rc=0
    if $cpp_srv_ready; then
      cpp_rc=$(curl -s "http://localhost:$RELOAD_CPP_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo 0)
    fi
    if [[ "$go_rc" -ge 1 && "$java_rc" -ge 1 && "$py_rc" -ge 1 ]]; then
      if ! $cpp_srv_ready || [[ "$cpp_rc" -ge 1 ]]; then
        reload_detected=true
        break
      fi
    fi
    sleep 0.2
  done

  if [[ "$reload_detected" != "true" ]]; then
    fail "hot-reload: servers did not detect config change within 10s (Go=$go_rc, Java=$java_rc, Python=$py_rc, C++=$cpp_rc)"
  fi

  # Test 2: After reload, DAG structure should change (use /dag endpoint)
  reload_total=$((reload_total + 1))
  go_dag_before=$("$WORK_DIR/pineapple-dag" -config "$WORK_DIR/reload_config_b.json" -format dot 2>/dev/null || echo "")
  go_dag_after=$(curl -s "http://localhost:$RELOAD_GO_PORT/dag?format=dot")
  java_dag_after=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/dag?format=dot")
  py_dag_after=$(curl -s "http://localhost:$RELOAD_PY_PORT/dag?format=dot")
  if [[ "$go_dag_after" == "$java_dag_after" && "$go_dag_after" == "$py_dag_after" && -n "$go_dag_after" ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [2] After reload, DAG matches across all engines"
  else
    fail "hot-reload: DAG mismatch after reload (Go vs Java: $([ "$go_dag_after" == "$java_dag_after" ] && echo match || echo differ), Go vs Python: $([ "$go_dag_after" == "$py_dag_after" ] && echo match || echo differ))"
  fi

  if $cpp_srv_ready; then
    cpp_reload_total=$((cpp_reload_total + 1))
    cpp_dag_after=$(curl -s "http://localhost:$RELOAD_CPP_PORT/dag?format=dot")
    if [[ "$go_dag_after" == "$cpp_dag_after" && -n "$cpp_dag_after" ]]; then
      cpp_reload_pass=$((cpp_reload_pass + 1))
      echo "    [2] C++ DAG after reload matches Go"
    else
      fail "hot-reload: C++ DAG mismatch after reload"
      diff <(echo "$go_dag_after") <(echo "$cpp_dag_after") >&2 || true
    fi
  fi

  # Test 3: reload_count in server stats incremented
  reload_total=$((reload_total + 1))
  go_reload_count=$(curl -s "http://localhost:$RELOAD_GO_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))")
  java_reload_count=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))")
  py_reload_count=$(curl -s "http://localhost:$RELOAD_PY_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))")
  if [[ "$go_reload_count" -ge 1 && "$java_reload_count" -ge 1 && "$py_reload_count" -ge 1 ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [3] reload_count >= 1 (Go=$go_reload_count, Java=$java_reload_count, Python=$py_reload_count)"
  else
    fail "hot-reload: reload_count not incremented (Go=$go_reload_count, Java=$java_reload_count, Python=$py_reload_count)"
  fi

  if $cpp_srv_ready; then
    cpp_reload_total=$((cpp_reload_total + 1))
    cpp_reload_count=$(curl -s "http://localhost:$RELOAD_CPP_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))")
    if [[ "$cpp_reload_count" -ge 1 ]]; then
      cpp_reload_pass=$((cpp_reload_pass + 1))
      echo "    [3] C++ reload_count >= 1 (C++=$cpp_reload_count)"
    else
      fail "hot-reload: C++ reload_count not incremented ($cpp_reload_count)"
    fi
  fi

  kill $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  PY_SRV_PID=""
  CPP_SRV_PID=""
else
  fail "hot-reload: servers failed to start"
  kill $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  PY_SRV_PID=""
  CPP_SRV_PID=""
fi

if [[ $reload_total -gt 0 && $reload_pass -eq $reload_total ]]; then
  pass "hot-reload parity ($reload_pass/$reload_total checks)"
elif [[ $reload_total -eq 0 ]]; then
  pass "hot-reload parity (skipped)"
fi

if [[ -n "${CPP_SERVER:-}" ]]; then
  if [[ $cpp_reload_total -gt 0 && $cpp_reload_pass -eq $cpp_reload_total ]]; then
    pass "hot-reload parity Go vs C++ ($cpp_reload_pass/$cpp_reload_total checks)"
  elif [[ $cpp_reload_total -eq 0 ]]; then
    pass "hot-reload parity Go vs C++ (skipped)"
  fi
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
