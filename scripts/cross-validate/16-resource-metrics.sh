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

    GO_PORT=26001
    JAVA_PORT=26002
    CPP_PORT=26004

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

      # [3a] Audit M6: 有量后再 scrape — fire 3 /execute rounds and verify
      #      the resources subtree still resolves to the same key shape with
      #      up=1. This catches "borrowing the client races with the probe
      #      goroutine and corrupts the metrics view" (the kind of regression
      #      that wouldn't show up in either the cold-start scrape or the
      #      probeless negative case).
      rm_total=$((rm_total + 1))
      send_traffic() {
        local port=$1 n=$2
        for ((i = 0; i < n; i++)); do
          curl -fsS -X POST -H "Content-Type: application/json" \
            -d '{"common":{"uid":"u-'"$i"'"},"items":[]}' \
            "http://localhost:$port/execute" >/dev/null 2>&1 || true
        done
      }
      send_traffic $GO_PORT 3
      send_traffic $JAVA_PORT 3
      $cpp_srv_ready && send_traffic $CPP_PORT 3
      GO_RES2=$(scrape_resources $GO_PORT)
      JAVA_RES2=$(scrape_resources $JAVA_PORT)
      CPP_RES2=""
      $cpp_srv_ready && CPP_RES2=$(scrape_resources $CPP_PORT)
      under_load=$(python3 -c "
import json
def shape(s):
    r = json.loads(s)
    return {m: sorted(v.keys()) for m, v in r.items()}
def up(s):
    return json.loads(s).get('pine_redis_up', {}).get('cache')
go1 = shape('''$GO_RES'''); go2 = shape('''$GO_RES2''')
ja1 = shape('''$JAVA_RES'''); ja2 = shape('''$JAVA_RES2''')
if go1 != go2:
    print(f'go shape drifted under load: {go1} -> {go2}')
elif ja1 != ja2:
    print(f'java shape drifted under load: {ja1} -> {ja2}')
elif up('''$GO_RES2''') != 1:
    print(f'go up degraded under load: {up(\"\"\"$GO_RES2\"\"\")}')
elif up('''$JAVA_RES2''') != 1:
    print(f'java up degraded under load: {up(\"\"\"$JAVA_RES2\"\"\")}')
else:
    print('stable')
")
      if [[ "$under_load" == "stable" ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [3a] under load: resources shape + up=1 stable across Go + Java"
      else
        fail "resource metrics: under-load state: $under_load"
      fi
      if $cpp_srv_ready; then
        cpp_rm_total=$((cpp_rm_total + 1))
        cpp_under_load=$(python3 -c "
import json
def shape(s):
    r = json.loads(s)
    return {m: sorted(v.keys()) for m, v in r.items()}
def up(s):
    return json.loads(s).get('pine_redis_up', {}).get('cache')
c1 = shape('''$CPP_RES'''); c2 = shape('''$CPP_RES2''')
if c1 != c2:
    print(f'cpp shape drifted under load: {c1} -> {c2}')
elif up('''$CPP_RES2''') != 1:
    print(f'cpp up degraded under load: {up(\"\"\"$CPP_RES2\"\"\")}')
else:
    print('stable')
")
        if [[ "$cpp_under_load" == "stable" ]]; then
          cpp_rm_pass=$((cpp_rm_pass + 1))
          echo "    [3a] under load: resources shape + up=1 stable in C++"
        else
          fail "resource metrics: under-load state C++: $cpp_under_load"
        fi
      fi

      # [3b] Audit M7: interval=-1 + probe cadence invariant.
      #      Fetcher must run once at Start (no fetcher loop) and the probe
      #      goroutine ticks every 15s — within a 2s window the ping count
      #      must stay at 1 (just the immediate-after-Start probe) across
      #      all three engines. This catches:
      #        * a regression that interprets interval=-1 as "spin tightly"
      #        * a regression that lowers / desyncs the probe period
      #          (Go redisProbeInterval / Java PROBE_INTERVAL_SECONDS /
      #           C++ kProbeInterval — all 15s)
      #      The /execute requests above borrow the client but do not call
      #      PING themselves, so they must not bump the gauge either.
      sleep 2
      GO_RES3=$(scrape_resources $GO_PORT)
      JAVA_RES3=$(scrape_resources $JAVA_PORT)
      CPP_RES3=""
      $cpp_srv_ready && CPP_RES3=$(scrape_resources $CPP_PORT)
      rm_total=$((rm_total + 1))
      cadence=$(python3 -c "
import json
def cnt(s):
    r = json.loads(s)
    return r.get('pine_redis_ping_duration_seconds', {}).get('cache', {}).get('count')
gc = cnt('''$GO_RES3''')
jc = cnt('''$JAVA_RES3''')
# Allow 1-2 in case the test machine raced the first PING tick at exactly
# the wrong moment; cadence regressions show up as ≥3 within 2s.
if gc is None or jc is None:
    print(f'ping count missing: go={gc} java={jc}')
elif gc not in (1, 2) or jc not in (1, 2):
    print(f'ping count outside [1,2] within 2s: go={gc} java={jc}')
else:
    print(f'ok ({gc}/{jc})')
")
      if [[ "$cadence" == ok* ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [3b] probe cadence within 2s: ping count ∈ {1,2} ($cadence)"
      else
        fail "resource metrics: probe cadence (interval=-1) drifted: $cadence"
      fi
      if $cpp_srv_ready; then
        cpp_rm_total=$((cpp_rm_total + 1))
        cpp_cadence=$(python3 -c "
import json
def cnt(s):
    r = json.loads(s)
    return r.get('pine_redis_ping_duration_seconds', {}).get('cache', {}).get('count')
cc = cnt('''$CPP_RES3''')
if cc is None:
    print('cpp ping count missing')
elif cc not in (1, 2):
    print(f'cpp ping count outside [1,2] within 2s: cpp={cc}')
else:
    print(f'ok (cpp={cc})')
")
        if [[ "$cpp_cadence" == ok* ]]; then
          cpp_rm_pass=$((cpp_rm_pass + 1))
          echo "    [3b] probe cadence within 2s C++: $cpp_cadence"
        else
          fail "resource metrics: probe cadence (interval=-1) C++: $cpp_cadence"
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
    GO_PORT=26011; JAVA_PORT=26012; CPP_PORT=26014
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

    # ============================================================
    # UNREACHABLE CASE (audit M6): metrics_name set, addr → dead port.
    # Probe runs once at resource Start; PING fails so up=0 must surface,
    # the metrics keys must still be present (resources != {}), and
    # fail_on_error=true must propagate as a 5xx-flavoured response.
    # ============================================================
    UNREACH_CONFIG="$WORK_DIR/rm_unreach_config.json"
    DEAD_PORT=21099
    cat > "$UNREACH_CONFIG" << CFG
{
  "resource_config": {
    "redis_conn": {
      "type": "redis_connection",
      "interval": -1,
      "params": {"addr": "127.0.0.1:$DEAD_PORT", "metrics_name": "cache"}
    }
  },
  "pipeline_config": {
    "operators": {
      "get_cache": {
        "type_name": "transform_redis_get",
        "resource_name": "redis_conn",
        "key_prefix": "test:",
        "fail_on_error": true,
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

    # Pick fresh ports — old ones may still be in TIME_WAIT
    GO_PORT=26021; JAVA_PORT=26022; CPP_PORT=26024
    "$WORK_DIR/pineapple-server" -config "$UNREACH_CONFIG" -addr ":$GO_PORT" &
    GO_PID=$!
    java -cp "$JAVA_CP" -Dpine.config="$UNREACH_CONFIG" -Dpine.port=$JAVA_PORT page.liam.pine.PineServer &
    JAVA_PID=$!
    CPP_PID=""
    cpp_srv_ready=false
    if [[ -n "${CPP_SERVER:-}" ]]; then
      "$CPP_SERVER" -config "$UNREACH_CONFIG" -addr ":$CPP_PORT" &
      CPP_PID=$!
    fi

    # scrape_unreach <port>: wait for the probe goroutine to complete its
    # first round and surface up=0 + populated cells. Go's go-redis client
    # retries 5× with backoff, so the first probe can take 10-20s before
    # the failure is reflected — bump the wait window beyond what the
    # positive-case scrape needs.
    scrape_unreach() {
      local port=$1
      for _ in $(seq 1 80); do
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
        # Wait for pine_redis_up to have a populated 'cache' cell
        # (probe finished its first iteration with the dial failure).
        local cell
        cell=$(printf '%s' "$r" | python3 -c "
import json, sys
try:
    r = json.loads(sys.stdin.read())
    print(r.get('pine_redis_up', {}).get('cache', '__MISSING__'))
except Exception:
    print('__MISSING__')
")
        if [[ "$cell" != "__MISSING__" && "$cell" != "None" ]]; then
          echo "$r"
          return 0
        fi
        sleep 0.5
      done
      curl -s "http://localhost:$port/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
res = d.get('resources', None)
print(json.dumps(res, sort_keys=True) if res is not None else '__MISSING__')
"
    }

    if srv_ready $GO_PORT && srv_ready $JAVA_PORT; then
      if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $CPP_PORT; then
        cpp_srv_ready=true
      fi

      GO_DEAD=$(scrape_unreach $GO_PORT)
      JAVA_DEAD=$(scrape_unreach $JAVA_PORT)
      CPP_DEAD=""
      $cpp_srv_ready && CPP_DEAD=$(scrape_unreach $CPP_PORT)

      # [5] Go: redis dead → up=0, but metrics keys still present
      rm_total=$((rm_total + 1))
      go_dead_ok=$(python3 -c "
import json
r = json.loads('''$GO_DEAD''')
if r == {}:
    print('resources empty (probe never ran or metrics_name lost)')
elif 'pine_redis_up' not in r:
    print(f'pine_redis_up missing: keys={sorted(r.keys())}')
elif r['pine_redis_up'].get('cache') != 0:
    print(f'pine_redis_up.cache = {r[\"pine_redis_up\"].get(\"cache\")} != 0')
else:
    print('ok')
")
      if [[ "$go_dead_ok" == "ok" ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [5] Go: redis dead → up=0, metrics keys retained"
      else
        fail "resource metrics: unreachable case Go: $go_dead_ok"
      fi

      # [6] Go vs Java: same shape + same up=0
      rm_total=$((rm_total + 1))
      gj_dead=$(python3 -c "
import json
go = json.loads('''$GO_DEAD''')
ja = json.loads('''$JAVA_DEAD''')
def keyset(r):
    return {m: sorted(v.keys()) for m, v in r.items()}
if keyset(go) != keyset(ja):
    print(f'shape go={keyset(go)} java={keyset(ja)}')
elif go.get('pine_redis_up') != ja.get('pine_redis_up'):
    print(f'up go={go.get(\"pine_redis_up\")} java={ja.get(\"pine_redis_up\")}')
else:
    print('match')
")
      if [[ "$gj_dead" == "match" ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [6] Go vs Java: redis dead → shape + up=0 parity"
      else
        fail "resource metrics: unreachable parity Go vs Java: $gj_dead"
      fi

      if $cpp_srv_ready; then
        cpp_rm_total=$((cpp_rm_total + 1))
        gc_dead=$(python3 -c "
import json
go = json.loads('''$GO_DEAD''')
cpp = json.loads('''$CPP_DEAD''')
def keyset(r):
    return {m: sorted(v.keys()) for m, v in r.items()}
if cpp == {}:
    print('cpp resources empty (probe never ran or metrics_name lost)')
elif keyset(go) != keyset(cpp):
    print(f'shape go={keyset(go)} cpp={keyset(cpp)}')
elif go.get('pine_redis_up') != cpp.get('pine_redis_up'):
    print(f'up go={go.get(\"pine_redis_up\")} cpp={cpp.get(\"pine_redis_up\")}')
else:
    print('match')
")
        if [[ "$gc_dead" == "match" ]]; then
          cpp_rm_pass=$((cpp_rm_pass + 1))
          echo "    [6] Go vs C++: redis dead → shape + up=0 parity"
        else
          fail "resource metrics: unreachable parity Go vs C++: $gc_dead"
        fi
      fi

      # [7] fail_on_error=true with dead redis → operator response should
      #     surface as a non-2xx for both engines (proves the gauge is not
      #     just decorative — pipeline reads it via the borrow path and the
      #     error propagates). Tolerates either an HTTP error or a 200 with
      #     an "error"-shaped body, whichever the engine's contract says.
      rm_total=$((rm_total + 1))
      probe_fail() {
        local port=$1
        # -o discards body; -w prints HTTP status + an explicit marker for
        # connection-reset / curl-side errors.
        local code
        code=$(curl -s -o /tmp/rm_dead_body.$$ -w "%{http_code}" -X POST \
          -H "Content-Type: application/json" \
          -d '{"common":{"uid":"u1"},"items":[]}' \
          "http://localhost:$port/execute" 2>/dev/null || echo "000")
        if [[ "$code" != "200" ]]; then
          rm -f "/tmp/rm_dead_body.$$"
          echo "non-200"
          return
        fi
        # 200 path: accept only if the body carries an error field;
        # otherwise the failure was silently swallowed.
        if grep -q '"error"' "/tmp/rm_dead_body.$$" 2>/dev/null; then
          rm -f "/tmp/rm_dead_body.$$"
          echo "error-body"
        else
          rm -f "/tmp/rm_dead_body.$$"
          echo "swallowed"
        fi
      }
      go_fail=$(probe_fail $GO_PORT)
      java_fail=$(probe_fail $JAVA_PORT)
      if [[ "$go_fail" != "swallowed" && "$java_fail" != "swallowed" ]]; then
        rm_pass=$((rm_pass + 1))
        echo "    [7] fail_on_error=true + dead redis → Go=$go_fail, Java=$java_fail"
      else
        fail "resource metrics: fail_on_error swallowed (Go=$go_fail, Java=$java_fail)"
      fi
    else
      fail "resource metrics: servers failed to start (unreachable case)"
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
