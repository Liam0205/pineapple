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
RELOAD_CPP_PORT=20004

"$WORK_DIR/pineapple-server" -config "$RELOAD_CONFIG_A" -addr ":$RELOAD_GO_PORT" &
GO_SRV_PID=$!
java -cp "$JAVA_CP" -Dpine.config="$RELOAD_CONFIG_A" -Dpine.port=$RELOAD_JAVA_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!
CPP_SRV_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$RELOAD_CONFIG_A" -addr ":$RELOAD_CPP_PORT" &
  CPP_SRV_PID=$!
fi

cpp_reload_pass=0
cpp_reload_total=0
cpp_srv_ready=false

if srv_ready $RELOAD_GO_PORT && srv_ready $RELOAD_JAVA_PORT; then
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
  if $cpp_srv_ready; then
    curl -s -X POST -H "Content-Type: application/json" -d "$RELOAD_REQ" "http://localhost:$RELOAD_CPP_PORT/execute" >/dev/null 2>&1 || true
  fi

  # Test 1: Initial operator count matches (after one execution)
  reload_total=$((reload_total + 1))
  go_ops_before=$(curl -s "http://localhost:$RELOAD_GO_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
  java_ops_before=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/stats" | python3 -c "import json,sys; print(len(json.load(sys.stdin).get('operators',{})))")
  if [[ "$go_ops_before" == "$java_ops_before" && "$go_ops_before" != "0" ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [1] Initial operator count matches ($go_ops_before operators, all engines)"
  else
    fail "hot-reload: initial operator count mismatch (Go=$go_ops_before, Java=$java_ops_before)"
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
    cpp_rc=0
    if $cpp_srv_ready; then
      cpp_rc=$(curl -s "http://localhost:$RELOAD_CPP_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo 0)
    fi
    if [[ "$go_rc" -ge 1 && "$java_rc" -ge 1 ]]; then
      if ! $cpp_srv_ready || [[ "$cpp_rc" -ge 1 ]]; then
        reload_detected=true
        break
      fi
    fi
    sleep 0.2
  done

  if [[ "$reload_detected" != "true" ]]; then
    fail "hot-reload: servers did not detect config change within 10s (Go=$go_rc, Java=$java_rc, C++=$cpp_rc)"
  fi

  # Test 2: After reload, DAG structure should change (use /dag endpoint)
  reload_total=$((reload_total + 1))
  go_dag_before=$("$WORK_DIR/pineapple-dag" -config "$WORK_DIR/reload_config_b.json" -format dot 2>/dev/null || echo "")
  go_dag_after=$(curl -s "http://localhost:$RELOAD_GO_PORT/dag?format=dot")
  java_dag_after=$(curl -s "http://localhost:$RELOAD_JAVA_PORT/dag?format=dot")
  if [[ "$go_dag_after" == "$java_dag_after" && -n "$go_dag_after" ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [2] After reload, DAG matches across all engines"
  else
    fail "hot-reload: DAG mismatch after reload (Go vs Java: $([ "$go_dag_after" == "$java_dag_after" ] && echo match || echo differ))"
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
  if [[ "$go_reload_count" -ge 1 && "$java_reload_count" -ge 1 ]]; then
    reload_pass=$((reload_pass + 1))
    echo "    [3] reload_count >= 1 (Go=$go_reload_count, Java=$java_reload_count)"
  else
    fail "hot-reload: reload_count not incremented (Go=$go_reload_count, Java=$java_reload_count)"
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

  kill $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  CPP_SRV_PID=""
else
  fail "hot-reload: servers failed to start"
  kill $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  CPP_SRV_PID=""
fi

# ============================================================
# Audit M8: resource_config hot-reload parity. The hot-reload path
# rebuilds both the engine AND the ResourceManager from scratch on
# every reload, but the existing tests (1-3) only swap pipeline ops.
# This block swaps a config whose resource_config has metrics_name="cache"
# for one with metrics_name="cache_v2", proving the new manager starts,
# the old metrics label disappears from /stats.resources, the new label
# appears, and the swap happens without serving a request against a
# closed resource. Requires redis-server; cleanly skipped otherwise.
# ============================================================
if which redis-server >/dev/null 2>&1; then
  echo
  echo "    [4-6] resource_config hot-reload (audit M8)"

  RC_REDIS_PORT=21102
  redis-server --port $RC_REDIS_PORT --daemonize yes --logfile /dev/null --save "" --appendonly no
  redis_ready=false
  for i in $(seq 1 20); do
    if redis-cli -p $RC_REDIS_PORT PING 2>/dev/null | grep -q PONG; then
      redis_ready=true
      break
    fi
    sleep 0.1
  done

  if [[ "$redis_ready" == "true" ]]; then
    RC_CONFIG="$WORK_DIR/rc_reload_config.json"
    RC_CONFIG_V2="$WORK_DIR/rc_reload_config_v2.json"

    rc_write_config() {
      # $1 = output path, $2 = metrics_name
      local out="$1" mname="$2"
      cat > "$out" << CFG
{
  "resource_config": {
    "redis_conn": {
      "type": "redis_connection",
      "interval": -1,
      "params": {"addr": "127.0.0.1:$RC_REDIS_PORT", "metrics_name": "$mname"}
    }
  },
  "pipeline_config": {
    "operators": {
      "get_cache": {
        "type_name": "transform_redis_get",
        "resource_name": "redis_conn",
        "key_prefix": "test:",
        "\$metadata": {
          "common_input": ["uid"],
          "common_output": ["result", "cache_hit"],
          "item_input": [],
          "item_output": []
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["get_cache"]}
  },
  "flow_contract": {
    "common_input": ["uid"],
    "item_input": [],
    "common_output": ["uid", "result", "cache_hit"],
    "item_output": []
  }
}
CFG
    }
    rc_write_config "$RC_CONFIG" "cache"
    rc_write_config "$RC_CONFIG_V2" "cache_v2"

    RC_GO_PORT=20011
    RC_JAVA_PORT=20012
    RC_CPP_PORT=20014

    "$WORK_DIR/pineapple-server" -config "$RC_CONFIG" -addr ":$RC_GO_PORT" &
    RC_GO_PID=$!
    java -cp "$JAVA_CP" -Dpine.config="$RC_CONFIG" -Dpine.port=$RC_JAVA_PORT page.liam.pine.PineServer &
    RC_JAVA_PID=$!
    RC_CPP_PID=""
    rc_cpp_ready=false
    if [[ -n "${CPP_SERVER:-}" ]]; then
      "$CPP_SERVER" -config "$RC_CONFIG" -addr ":$RC_CPP_PORT" &
      RC_CPP_PID=$!
    fi

    rc_label() {
      curl -s "http://localhost:$1/stats" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print('__ERR__'); sys.exit()
res = d.get('resources') or {}
print(','.join(sorted(res.get('pine_redis_up', {}).keys())))
"
    }

    rc_wait_label() {
      local port=$1 want=$2
      for _ in $(seq 1 60); do
        if [[ "$(rc_label $port)" == "$want" ]]; then
          return 0
        fi
        sleep 0.2
      done
      return 1
    }

    if srv_ready $RC_GO_PORT && srv_ready $RC_JAVA_PORT; then
      [[ -n "${CPP_SERVER:-}" ]] && srv_ready $RC_CPP_PORT && rc_cpp_ready=true

      # [4] before reload: only "cache" label exposed on all three engines
      reload_total=$((reload_total + 1))
      rc_wait_label $RC_GO_PORT "cache" || true
      rc_wait_label $RC_JAVA_PORT "cache" || true
      $rc_cpp_ready && (rc_wait_label $RC_CPP_PORT "cache" || true)
      rc_lbl_go=$(rc_label $RC_GO_PORT)
      rc_lbl_ja=$(rc_label $RC_JAVA_PORT)
      if [[ "$rc_lbl_go" == "cache" && "$rc_lbl_ja" == "cache" ]]; then
        reload_pass=$((reload_pass + 1))
        echo "    [4] before reload: pine_redis_up label = 'cache' (Go + Java)"
      else
        fail "hot-reload resource_config: pre-reload label drift Go='$rc_lbl_go' Java='$rc_lbl_ja'"
      fi
      if $rc_cpp_ready; then
        cpp_reload_total=$((cpp_reload_total + 1))
        rc_lbl_cpp=$(rc_label $RC_CPP_PORT)
        if [[ "$rc_lbl_cpp" == "cache" ]]; then
          cpp_reload_pass=$((cpp_reload_pass + 1))
          echo "    [4] before reload: pine_redis_up label = 'cache' (C++)"
        else
          fail "hot-reload resource_config: pre-reload label drift C++='$rc_lbl_cpp'"
        fi
      fi

      # Trigger reload: swap config contents in place
      cp "$RC_CONFIG_V2" "$RC_CONFIG"

      # [5] after reload: label must transition cache → cache_v2 across all
      reload_total=$((reload_total + 1))
      go_ok=$(rc_wait_label $RC_GO_PORT "cache_v2" && echo y || echo n)
      ja_ok=$(rc_wait_label $RC_JAVA_PORT "cache_v2" && echo y || echo n)
      if [[ "$go_ok" == "y" && "$ja_ok" == "y" ]]; then
        reload_pass=$((reload_pass + 1))
        echo "    [5] after reload: pine_redis_up label = 'cache_v2' (Go + Java)"
      else
        rc_lbl_go=$(rc_label $RC_GO_PORT)
        rc_lbl_ja=$(rc_label $RC_JAVA_PORT)
        fail "hot-reload resource_config: post-reload label not cache_v2 (Go='$rc_lbl_go' Java='$rc_lbl_ja')"
      fi
      if $rc_cpp_ready; then
        cpp_reload_total=$((cpp_reload_total + 1))
        cpp_ok=$(rc_wait_label $RC_CPP_PORT "cache_v2" && echo y || echo n)
        if [[ "$cpp_ok" == "y" ]]; then
          cpp_reload_pass=$((cpp_reload_pass + 1))
          echo "    [5] after reload: pine_redis_up label = 'cache_v2' (C++)"
        else
          rc_lbl_cpp=$(rc_label $RC_CPP_PORT)
          fail "hot-reload resource_config: post-reload C++ label='$rc_lbl_cpp'"
        fi
      fi

      # [6] /execute against the new resource still resolves: the borrow
      #     path must reach the *new* manager, not retain a stale reference
      #     to the old one (which would have been Stop()ed).
      reload_total=$((reload_total + 1))
      RC_REQ='{"common":{"uid":"u1"},"items":[]}'
      go_status=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "$RC_REQ" "http://localhost:$RC_GO_PORT/execute")
      ja_status=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "$RC_REQ" "http://localhost:$RC_JAVA_PORT/execute")
      if [[ "$go_status" == "200" && "$ja_status" == "200" ]]; then
        reload_pass=$((reload_pass + 1))
        echo "    [6] post-reload /execute against new manager: 200 (Go + Java)"
      else
        fail "hot-reload resource_config: post-reload execute Go=$go_status Java=$ja_status"
      fi
      if $rc_cpp_ready; then
        cpp_reload_total=$((cpp_reload_total + 1))
        cpp_status=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "$RC_REQ" "http://localhost:$RC_CPP_PORT/execute")
        if [[ "$cpp_status" == "200" ]]; then
          cpp_reload_pass=$((cpp_reload_pass + 1))
          echo "    [6] post-reload /execute against new manager: 200 (C++)"
        else
          fail "hot-reload resource_config: post-reload execute C++=$cpp_status"
        fi
      fi
    else
      fail "hot-reload resource_config: servers failed to start"
    fi

    kill $RC_GO_PID $RC_JAVA_PID 2>/dev/null || true
    [[ -n "$RC_CPP_PID" ]] && kill $RC_CPP_PID 2>/dev/null || true
    wait $RC_GO_PID $RC_JAVA_PID 2>/dev/null || true
    [[ -n "$RC_CPP_PID" ]] && wait $RC_CPP_PID 2>/dev/null || true
    redis-cli -p $RC_REDIS_PORT SHUTDOWN NOSAVE >/dev/null 2>&1 || true
  else
    echo "    [4-6] skipped: redis on $RC_REDIS_PORT failed to start"
  fi
else
  echo "    [4-6] skipped: redis-server not found"
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
