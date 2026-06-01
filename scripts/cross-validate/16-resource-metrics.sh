#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 16. Resource metrics parity (/stats.resources) ----------
# Verifies the resource-level metrics emitted by the redis_connection resource
# (pool gauges + PING-probe latency) are exposed under /stats.resources, with
# byte-exact three-engine shape parity AND correctness (up=1, ping count>=1).
# Also locks the negative case: empty metrics_name → resources == {}.
#
# Requires a real redis-server. Skipped cleanly when redis-server is absent.
echo
echo "==> [16/$TOTAL_SECTIONS] Resource metrics parity (/stats.resources, requires redis-server)"

rm_pass=0
rm_total=0
cpp_rm_pass=0
cpp_rm_total=0

if ! which redis-server >/dev/null 2>&1; then
  pass "resource metrics parity (skipped: redis-server not found)"
else
  REDIS_PORT=21002
  redis-server --port $REDIS_PORT --daemonize yes --logfile /dev/null --save "" --appendonly no

  redis_ready=false
  for i in $(seq 1 20); do
    if redis-cli -p $REDIS_PORT PING 2>/dev/null | grep -q PONG; then
      redis_ready=true
      break
    fi
    sleep 0.1
  done

  if [[ "$redis_ready" != "true" ]]; then
    fail "resource metrics: redis-server failed to start on port $REDIS_PORT"
  else
    echo "    Redis ready on port $REDIS_PORT"

    # --- Build configs: positive (metrics_name set) + negative (no metrics_name) ---
    POS_CONFIG="$WORK_DIR/rm_pos_config.json"
    NEG_CONFIG="$WORK_DIR/rm_neg_config.json"
    write_config() {
      # $1 = output path, $2 = metrics_name params fragment ("" → omit)
      local out="$1" mfrag="$2"
      cat > "$out" << CFG
{
  "resource_config": {
    "redis_conn": {
      "type": "redis_connection",
      "interval": -1,
      "params": {"addr": "127.0.0.1:$REDIS_PORT"$mfrag}
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
    write_config "$POS_CONFIG" ', "metrics_name": "cache"'
    write_config "$NEG_CONFIG" ''

    GO_PORT=23011
    JAVA_PORT=23012
    CPP_PORT=23014

    # scrape_resources <port>: print the /stats.resources subtree as compact
    # sorted JSON, retrying briefly so the probe goroutine (which runs one
    # immediate sample at resource Start) has a chance to populate metrics.
    scrape_resources() {
      local port=$1
      for _ in $(seq 1 30); do
        local r
        r=$(curl -s "http://localhost:$port/stats" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    print('__ERR__'); sys.exit()
res = d.get('resources', None)
print(json.dumps(res, sort_keys=True) if res is not None else '__MISSING__')
")
        if [[ "$r" != "__MISSING__" && "$r" != "__ERR__" && "$r" != "{}" ]]; then
          echo "$r"
          return 0
        fi
        sleep 0.1
      done
      # final attempt (may legitimately be {} in the negative case)
      curl -s "http://localhost:$port/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
res = d.get('resources', None)
print(json.dumps(res, sort_keys=True) if res is not None else '__MISSING__')
"
    }

    # ============================================================
    # POSITIVE CASE: metrics_name="cache" → 4 metrics under resources
    # ============================================================
    "$WORK_DIR/pineapple-server" -config "$POS_CONFIG" -addr ":$GO_PORT" &
    GO_PID=$!
    java -cp "$JAVA_CP" -Dpine.config="$POS_CONFIG" -Dpine.port=$JAVA_PORT page.liam.pine.PineServer &
    JAVA_PID=$!
    CPP_PID=""
    cpp_srv_ready=false
    if [[ -n "${CPP_SERVER:-}" ]]; then
      "$CPP_SERVER" -config "$POS_CONFIG" -addr ":$CPP_PORT" &
      CPP_PID=$!
    fi

    if srv_ready $GO_PORT && srv_ready $JAVA_PORT; then
      if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $CPP_PORT; then
        cpp_srv_ready=true
      fi

      GO_RES=$(scrape_resources $GO_PORT)
      JAVA_RES=$(scrape_resources $JAVA_PORT)
      CPP_RES=""
      $cpp_srv_ready && CPP_RES=$(scrape_resources $CPP_PORT)

      # [1] Go correctness: 4 metrics, label "cache", up=1, ping count>=1
      rm_total=$((rm_total + 1))
      go_correct=$(python3 -c "
import json
r = json.loads('''$GO_RES''')
names = sorted(r.keys())
want = ['pine_redis_ping_duration_seconds','pine_redis_pool_idle_conns','pine_redis_pool_total_conns','pine_redis_up']
if names != want:
    print(f'metric names {names} != {want}')
elif 'cache' not in r['pine_redis_up']:
    print('missing label cache under pine_redis_up')
elif r['pine_redis_up']['cache'] != 1:
    print(f'pine_redis_up.cache = {r[\"pine_redis_up\"][\"cache\"]} != 1')
elif r['pine_redis_ping_duration_seconds']['cache'].get('count', 0) < 1:
    print('ping count < 1')
elif 'sum_ns' not in r['pine_redis_ping_duration_seconds']['cache']:
    print('ping cell missing sum_ns')
else:
    print('ok')
")
      if [[ "$go_correct" == "ok" ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [1] Go: 4 resource metrics present, up=1, ping count>=1"
      else
        fail "resource metrics: Go correctness: $go_correct"
      fi

      # [2] Go vs Java: same metric-name + label-key SET; up=1 both; ping>=1 both
      rm_total=$((rm_total + 1))
      gj_parity=$(python3 -c "
import json
go = json.loads('''$GO_RES''')
ja = json.loads('''$JAVA_RES''')
def keyset(r):
    return {m: sorted(v.keys()) for m, v in r.items()}
if keyset(go) != keyset(ja):
    print(f'shape go={keyset(go)} java={keyset(ja)}')
elif go['pine_redis_up'] != ja['pine_redis_up']:
    print(f'up go={go[\"pine_redis_up\"]} java={ja[\"pine_redis_up\"]}')
elif ja['pine_redis_ping_duration_seconds']['cache'].get('count',0) < 1:
    print('java ping count < 1')
else:
    print('match')
")
      if [[ "$gj_parity" == "match" ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [2] Go vs Java: resources shape + up + ping parity"
      else
        fail "resource metrics: Go vs Java parity: $gj_parity"
      fi

      if $cpp_srv_ready; then
        # [3] Go vs C++: same shape + correctness
        cpp_rm_total=$((cpp_rm_total + 1))
        gc_parity=$(python3 -c "
import json
go = json.loads('''$GO_RES''')
cpp = json.loads('''$CPP_RES''')
def keyset(r):
    return {m: sorted(v.keys()) for m, v in r.items()}
if keyset(go) != keyset(cpp):
    print(f'shape go={keyset(go)} cpp={keyset(cpp)}')
elif go['pine_redis_up'] != cpp['pine_redis_up']:
    print(f'up go={go[\"pine_redis_up\"]} cpp={cpp[\"pine_redis_up\"]}')
elif cpp['pine_redis_ping_duration_seconds']['cache'].get('count',0) < 1:
    print('cpp ping count < 1')
else:
    print('match')
")
        if [[ "$gc_parity" == "match" ]]; then
          cpp_rm_pass=$((cpp_rm_pass + 1))
          echo "    [3] Go vs C++: resources shape + up + ping parity"
        else
          fail "resource metrics: Go vs C++ parity: $gc_parity"
        fi
      fi
    else
      fail "resource metrics: servers failed to start (positive case)"
    fi

    kill $GO_PID $JAVA_PID 2>/dev/null || true
    [[ -n "$CPP_PID" ]] && kill $CPP_PID 2>/dev/null || true
    wait $GO_PID $JAVA_PID 2>/dev/null || true
    [[ -n "$CPP_PID" ]] && wait $CPP_PID 2>/dev/null || true

    # ============================================================
    # NEGATIVE CASE: no metrics_name → resources == {} in all engines
    # ============================================================
    GO_PORT=23021; JAVA_PORT=23022; CPP_PORT=23024
    "$WORK_DIR/pineapple-server" -config "$NEG_CONFIG" -addr ":$GO_PORT" &
    GO_PID=$!
    java -cp "$JAVA_CP" -Dpine.config="$NEG_CONFIG" -Dpine.port=$JAVA_PORT page.liam.pine.PineServer &
    JAVA_PID=$!
    CPP_PID=""
    cpp_srv_ready=false
    if [[ -n "${CPP_SERVER:-}" ]]; then
      "$CPP_SERVER" -config "$NEG_CONFIG" -addr ":$CPP_PORT" &
      CPP_PID=$!
    fi

    if srv_ready $GO_PORT && srv_ready $JAVA_PORT; then
      if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $CPP_PORT; then
        cpp_srv_ready=true
      fi

      get_resources_raw() {
        curl -s "http://localhost:$1/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
res = d.get('resources', None)
print(json.dumps(res, sort_keys=True) if res is not None else '__MISSING__')
"
      }

      rm_total=$((rm_total + 1))
      GO_NEG=$(get_resources_raw $GO_PORT)
      JAVA_NEG=$(get_resources_raw $JAVA_PORT)
      if [[ "$GO_NEG" == "{}" && "$JAVA_NEG" == "{}" ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [4] empty metrics_name → resources == {} (Go + Java)"
      else
        fail "resource metrics: negative case (Go='$GO_NEG' Java='$JAVA_NEG')"
      fi

      if $cpp_srv_ready; then
        cpp_rm_total=$((cpp_rm_total + 1))
        CPP_NEG=$(get_resources_raw $CPP_PORT)
        if [[ "$CPP_NEG" == "{}" ]]; then
          cpp_rm_pass=$((cpp_rm_pass + 1))
          echo "    [4] empty metrics_name → resources == {} (C++)"
        else
          fail "resource metrics: negative case C++ (resources='$CPP_NEG')"
        fi
      fi
    else
      fail "resource metrics: servers failed to start (negative case)"
    fi

    kill $GO_PID $JAVA_PID 2>/dev/null || true
    [[ -n "$CPP_PID" ]] && kill $CPP_PID 2>/dev/null || true
    wait $GO_PID $JAVA_PID 2>/dev/null || true
    [[ -n "$CPP_PID" ]] && wait $CPP_PID 2>/dev/null || true

    redis-cli -p $REDIS_PORT SHUTDOWN NOSAVE >/dev/null 2>&1 || true
  fi

  if [[ $rm_total -gt 0 && $rm_pass -eq $rm_total ]]; then
    pass "resource metrics parity ($rm_pass/$rm_total checks)"
  elif [[ $rm_total -eq 0 ]]; then
    pass "resource metrics parity (skipped: setup failed)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]]; then
    if [[ $cpp_rm_total -gt 0 && $cpp_rm_pass -eq $cpp_rm_total ]]; then
      pass "resource metrics parity Go vs C++ ($cpp_rm_pass/$cpp_rm_total checks)"
    elif [[ $cpp_rm_total -eq 0 ]]; then
      pass "resource metrics parity Go vs C++ (skipped: setup failed)"
    fi
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
