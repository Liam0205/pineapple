#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 20. Custom routes parity (issue #169) ----------
# Verifies the Route/Ingress/Egress extension point and the -watch toggle
# behave identically across the three engines. Servers run with
# -demo-routes (registers POST /api/echo) and -watch=false.
echo
echo "==> [20/$TOTAL_SECTIONS] Custom routes parity (demo route, watch toggle)"

CR_FIXTURE="$REPO_ROOT/fixtures/pipelines/transform_then_filter.json"
CR_CONFIG="$WORK_DIR/custom_routes_config.json"
python3 -c "
import json
with open('$CR_FIXTURE') as f:
    data = json.load(f)
with open('$CR_CONFIG', 'w') as cf:
    json.dump(data.get('config', {}), cf)
"

GO_CR_PORT=22101
JAVA_CR_PORT=22102
CPP_CR_PORT=22104

"$WORK_DIR/pineapple-server" -config "$CR_CONFIG" -addr ":$GO_CR_PORT" -demo-routes -watch=false &
GO_CR_PID=$!

java -cp "$JAVA_CP" -Dpine.config="$CR_CONFIG" -Dpine.port=$JAVA_CR_PORT \
  -Dpine.demoRoutes=true -Dpine.watch=false page.liam.pine.PineServer &
JAVA_CR_PID=$!

CPP_CR_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$CR_CONFIG" -addr ":$CPP_CR_PORT" -demo-routes -watch=false &
  CPP_CR_PID=$!
fi

cr_cleanup() {
  [[ -n "${GO_CR_PID:-}" ]] && kill $GO_CR_PID 2>/dev/null || true
  [[ -n "${JAVA_CR_PID:-}" ]] && kill $JAVA_CR_PID 2>/dev/null || true
  [[ -n "${CPP_CR_PID:-}" ]] && kill $CPP_CR_PID 2>/dev/null || true
  wait $GO_CR_PID 2>/dev/null || true
  wait $JAVA_CR_PID 2>/dev/null || true
  [[ -n "${CPP_CR_PID:-}" ]] && wait $CPP_CR_PID 2>/dev/null || true
  GO_CR_PID=""
  JAVA_CR_PID=""
  CPP_CR_PID=""
}
trap 'cr_cleanup' EXIT

cr_pass=0
cr_total=0
cpp_cr_pass=0
cpp_cr_total=0
cpp_cr_ready=false

CR_BODY='{"common":{"boost":2.0},"items":[{"id":1.0},{"id":2.0}]}'

if ! srv_ready $GO_CR_PORT; then
  fail "custom-routes: Go server failed to start"
  cr_cleanup
elif ! srv_ready $JAVA_CR_PORT; then
  fail "custom-routes: Java server failed to start"
  cr_cleanup
else
  echo "    Go and Java servers ready."
  if [[ -n "${CPP_SERVER:-}" ]]; then
    # A configured C++ binary that fails readiness is a hard failure — it
    # must not silently drop the C++ arm (zero checks would still "pass").
    if srv_ready $CPP_CR_PORT; then
      cpp_cr_ready=true
      echo "    C++ server also ready."
    else
      fail "custom-routes: C++ server failed to start (CPP_SERVER is set)"
    fi
  fi

  # Test 1: POST /api/echo with a valid body → 200 + identical body
  cr_total=$((cr_total + 1))
  go_echo=$(curl -s -X POST "http://localhost:$GO_CR_PORT/api/echo" -d "$CR_BODY" | normalize_json)
  java_echo=$(curl -s -X POST "http://localhost:$JAVA_CR_PORT/api/echo" -d "$CR_BODY" | normalize_json)
  go_echo_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$GO_CR_PORT/api/echo" -d "$CR_BODY")
  java_echo_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$JAVA_CR_PORT/api/echo" -d "$CR_BODY")
  if [[ "$go_echo_code" == "200" && "$java_echo_code" == "200" && "$go_echo" == "$java_echo" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [1] POST /api/echo → 200 + body parity (Go, Java)"
  else
    fail "custom-routes: echo (Go=$go_echo_code/$go_echo, Java=$java_echo_code/$java_echo)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_echo=$(curl -s -X POST "http://localhost:$CPP_CR_PORT/api/echo" -d "$CR_BODY" | normalize_json)
    cpp_echo_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$CPP_CR_PORT/api/echo" -d "$CR_BODY")
    if [[ "$cpp_echo_code" == "200" && "$go_echo" == "$cpp_echo" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [1] C++ POST /api/echo → 200 + body matches Go"
    else
      fail "custom-routes: C++ echo (code=$cpp_echo_code, Go=$go_echo, C++=$cpp_echo)"
    fi
  fi

  # Test 2: wrong method on custom route → 405 + identical error body
  cr_total=$((cr_total + 1))
  go_405=$(curl -s "http://localhost:$GO_CR_PORT/api/echo" | normalize_json)
  java_405=$(curl -s "http://localhost:$JAVA_CR_PORT/api/echo" | normalize_json)
  go_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_CR_PORT/api/echo")
  java_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_CR_PORT/api/echo")
  if [[ "$go_405_code" == "405" && "$java_405_code" == "405" && "$go_405" == "$java_405" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [2] GET /api/echo → 405 + body parity (Go, Java)"
  else
    fail "custom-routes: 405 (Go=$go_405_code/$go_405, Java=$java_405_code/$java_405)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_405=$(curl -s "http://localhost:$CPP_CR_PORT/api/echo" | normalize_json)
    cpp_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$CPP_CR_PORT/api/echo")
    if [[ "$cpp_405_code" == "405" && "$go_405" == "$cpp_405" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [2] C++ GET /api/echo → 405 matches Go"
    else
      fail "custom-routes: C++ 405 (code=$cpp_405_code, Go=$go_405, C++=$cpp_405)"
    fi
  fi

  # Test 3: malformed body → Ingress error → Egress-written 400
  cr_total=$((cr_total + 1))
  go_400=$(curl -s -X POST "http://localhost:$GO_CR_PORT/api/echo" -d '{invalid' | normalize_json)
  java_400=$(curl -s -X POST "http://localhost:$JAVA_CR_PORT/api/echo" -d '{invalid' | normalize_json)
  go_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$GO_CR_PORT/api/echo" -d '{invalid')
  java_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$JAVA_CR_PORT/api/echo" -d '{invalid')
  if [[ "$go_400_code" == "400" && "$java_400_code" == "400" && "$go_400" == "$java_400" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [3] POST /api/echo malformed → 400 + body parity (Go, Java)"
  else
    fail "custom-routes: ingress error (Go=$go_400_code/$go_400, Java=$java_400_code/$java_400)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_400=$(curl -s -X POST "http://localhost:$CPP_CR_PORT/api/echo" -d '{invalid' | normalize_json)
    cpp_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$CPP_CR_PORT/api/echo" -d '{invalid')
    if [[ "$cpp_400_code" == "400" && "$go_400" == "$cpp_400" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [3] C++ POST /api/echo malformed → 400 matches Go"
    else
      fail "custom-routes: C++ ingress error (code=$cpp_400_code, Go=$go_400, C++=$cpp_400)"
    fi
  fi

  # Test 4: built-in endpoints unaffected by custom routes
  cr_total=$((cr_total + 1))
  go_health=$(curl -s "http://localhost:$GO_CR_PORT/health" | normalize_json)
  java_health=$(curl -s "http://localhost:$JAVA_CR_PORT/health" | normalize_json)
  go_exec_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$GO_CR_PORT/execute" -d "$CR_BODY")
  java_exec_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$JAVA_CR_PORT/execute" -d "$CR_BODY")
  if [[ "$go_health" == "$java_health" && "$go_exec_code" == "200" && "$java_exec_code" == "200" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [4] built-in /health + /execute unaffected (Go, Java)"
  else
    fail "custom-routes: built-ins (Go health=$go_health exec=$go_exec_code, Java health=$java_health exec=$java_exec_code)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_health=$(curl -s "http://localhost:$CPP_CR_PORT/health" | normalize_json)
    cpp_exec_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$CPP_CR_PORT/execute" -d "$CR_BODY")
    if [[ "$go_health" == "$cpp_health" && "$cpp_exec_code" == "200" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [4] C++ built-ins unaffected"
    else
      fail "custom-routes: C++ built-ins (health=$cpp_health exec=$cpp_exec_code)"
    fi
  fi

  # Test 5: unknown path still 404 with custom routes registered
  cr_total=$((cr_total + 1))
  go_404=$(curl -s "http://localhost:$GO_CR_PORT/api/unknown" | normalize_json)
  java_404=$(curl -s "http://localhost:$JAVA_CR_PORT/api/unknown" | normalize_json)
  go_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_CR_PORT/api/unknown")
  java_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_CR_PORT/api/unknown")
  if [[ "$go_404_code" == "404" && "$java_404_code" == "404" && "$go_404" == "$java_404" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [5] GET /api/unknown → 404 + body parity (Go, Java)"
  else
    fail "custom-routes: 404 (Go=$go_404_code/$go_404, Java=$java_404_code/$java_404)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_404=$(curl -s "http://localhost:$CPP_CR_PORT/api/unknown" | normalize_json)
    cpp_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$CPP_CR_PORT/api/unknown")
    if [[ "$cpp_404_code" == "404" && "$go_404" == "$cpp_404" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [5] C++ GET /api/unknown → 404 matches Go"
    else
      fail "custom-routes: C++ 404 (code=$cpp_404_code, Go=$go_404, C++=$cpp_404)"
    fi
  fi

  # Test 6: custom route path appears under its own label in /stats http
  # metrics (bounded known-path set includes custom routes, not "_other").
  cr_total=$((cr_total + 1))
  go_label=$(curl -s "http://localhost:$GO_CR_PORT/stats" | python3 -c "
import json, sys
reqs = json.load(sys.stdin).get('http', {}).get('requests_total', {})
print(any(k.startswith('POST /api/echo ') for k in reqs))
")
  java_label=$(curl -s "http://localhost:$JAVA_CR_PORT/stats" | python3 -c "
import json, sys
reqs = json.load(sys.stdin).get('http', {}).get('requests_total', {})
print(any(k.startswith('POST /api/echo ') for k in reqs))
")
  if [[ "$go_label" == "True" && "$java_label" == "True" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [6] /stats http metrics label custom route by path (Go, Java)"
  else
    fail "custom-routes: metrics label (Go=$go_label, Java=$java_label)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_label=$(curl -s "http://localhost:$CPP_CR_PORT/stats" | python3 -c "
import json, sys
reqs = json.load(sys.stdin).get('http', {}).get('requests_total', {})
print(any(k.startswith('POST /api/echo ') for k in reqs))
")
    if [[ "$cpp_label" == "True" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [6] C++ /stats http metrics label custom route by path"
    else
      fail "custom-routes: C++ metrics label ($cpp_label)"
    fi
  fi

  # Test 7: custom-route executions count in scheduler/operator stats.
  # Guards the C++ regression where custom routes bypassed the shared server
  # execution path and /stats.scheduler run_count stayed flat.
  cr_total=$((cr_total + 1))
  stats_counts() {
    curl -s "http://localhost:$1/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
run = d.get('scheduler', {}).get('run_count', 0)
execs = sum(op.get('exec_count', 0) for op in d.get('operators', {}).values())
print(f'{run} {execs}')
"
  }
  read -r go_run0 go_exec0 <<< "$(stats_counts $GO_CR_PORT)"
  read -r java_run0 java_exec0 <<< "$(stats_counts $JAVA_CR_PORT)"
  curl -s -X POST "http://localhost:$GO_CR_PORT/api/echo" -d "$CR_BODY" > /dev/null
  curl -s -X POST "http://localhost:$JAVA_CR_PORT/api/echo" -d "$CR_BODY" > /dev/null
  read -r go_run1 go_exec1 <<< "$(stats_counts $GO_CR_PORT)"
  read -r java_run1 java_exec1 <<< "$(stats_counts $JAVA_CR_PORT)"
  if [[ $((go_run1 - go_run0)) -ge 1 && $((go_exec1 - go_exec0)) -ge 1 &&
        $((java_run1 - java_run0)) -ge 1 && $((java_exec1 - java_exec0)) -ge 1 ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [7] /api/echo bumps scheduler run_count + operator exec_count (Go, Java)"
  else
    fail "custom-routes: stats increments (Go run ${go_run0}->${go_run1} exec ${go_exec0}->${go_exec1}, Java run ${java_run0}->${java_run1} exec ${java_exec0}->${java_exec1})"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    read -r cpp_run0 cpp_exec0 <<< "$(stats_counts $CPP_CR_PORT)"
    curl -s -X POST "http://localhost:$CPP_CR_PORT/api/echo" -d "$CR_BODY" > /dev/null
    read -r cpp_run1 cpp_exec1 <<< "$(stats_counts $CPP_CR_PORT)"
    if [[ $((cpp_run1 - cpp_run0)) -ge 1 && $((cpp_exec1 - cpp_exec0)) -ge 1 ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [7] C++ /api/echo bumps scheduler run_count + operator exec_count"
    else
      fail "custom-routes: C++ stats increments (run ${cpp_run0}->${cpp_run1} exec ${cpp_exec0}->${cpp_exec1})"
    fi
  fi

  # Test 8: oversized body on the custom route → central 413, Egress not run
  cr_total=$((cr_total + 1))
  BIG_BODY_FILE="$WORK_DIR/cr_big_body.json"
  python3 -c "
with open('$BIG_BODY_FILE', 'w') as f:
    f.write('{\"common\":{\"boost\":\"' + 'a' * (11 * 1024 * 1024) + '\"}}')
"
  go_413=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$GO_CR_PORT/api/echo" --data-binary "@$BIG_BODY_FILE")
  java_413=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$JAVA_CR_PORT/api/echo" --data-binary "@$BIG_BODY_FILE")
  go_413_body=$(curl -s -X POST "http://localhost:$GO_CR_PORT/api/echo" --data-binary "@$BIG_BODY_FILE" | normalize_json)
  java_413_body=$(curl -s -X POST "http://localhost:$JAVA_CR_PORT/api/echo" --data-binary "@$BIG_BODY_FILE" | normalize_json)
  if [[ "$go_413" == "413" && "$java_413" == "413" && "$go_413_body" == "$java_413_body" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [8] oversized POST /api/echo → 413 + body parity (Go, Java)"
  else
    fail "custom-routes: body limit (Go=$go_413/$go_413_body, Java=$java_413/$java_413_body)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_413=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$CPP_CR_PORT/api/echo" --data-binary "@$BIG_BODY_FILE")
    cpp_413_body=$(curl -s -X POST "http://localhost:$CPP_CR_PORT/api/echo" --data-binary "@$BIG_BODY_FILE" | normalize_json)
    if [[ "$cpp_413" == "413" && "$go_413_body" == "$cpp_413_body" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [8] C++ oversized POST /api/echo → 413 matches Go"
    else
      fail "custom-routes: C++ body limit (code=$cpp_413, Go=$go_413_body, C++=$cpp_413_body)"
    fi
  fi

  # Test 9: watch=false → touching the config does NOT bump reload_count
  cr_total=$((cr_total + 1))
  touch "$CR_CONFIG"
  sleep 3  # watcher interval is 2s in all three engines; 3s covers a tick
  go_rc=$(curl -s "http://localhost:$GO_CR_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo -1)
  java_rc=$(curl -s "http://localhost:$JAVA_CR_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo -1)
  if [[ "$go_rc" == "0" && "$java_rc" == "0" ]]; then
    cr_pass=$((cr_pass + 1))
    echo "    [9] watch=false → reload_count stays 0 after config touch (Go, Java)"
  else
    fail "custom-routes: watch toggle (Go reload_count=$go_rc, Java reload_count=$java_rc)"
  fi

  if $cpp_cr_ready; then
    cpp_cr_total=$((cpp_cr_total + 1))
    cpp_rc=$(curl -s "http://localhost:$CPP_CR_PORT/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo -1)
    if [[ "$cpp_rc" == "0" ]]; then
      cpp_cr_pass=$((cpp_cr_pass + 1))
      echo "    [9] C++ watch=false → reload_count stays 0"
    else
      fail "custom-routes: C++ watch toggle (reload_count=$cpp_rc)"
    fi
  fi

  cr_cleanup

  # Test 10: custom route under active hot-reload (watch=true). Guards the
  # C++ lock-window regression where the resource manager was snapshotted
  # outside the engine lock: a reload swapping engine_/resource_manager_
  # mid-request could race a concurrent custom-route execute (use-after-free
  # or old-resources-on-new-engine).
  #
  # Overlap is guaranteed, not assumed: background workers hammer /api/echo
  # continuously while the main thread touches the config and polls THIS
  # server's reload_count until it strictly increases past the baseline taken
  # at arm start — so at least one reload demonstrably completes while
  # requests are in flight. Each server gets its own config file so another
  # arm's touches can never pre-satisfy the counter.
  reload_count_of() {
    curl -s "http://localhost:$1/stats" | python3 -c "import json,sys; print(json.load(sys.stdin).get('server',{}).get('reload_count',0))" 2>/dev/null || echo 0
  }

  reload_race_arm() {
    # $1=port $2=per-server config file; returns 0 when reload_count strictly
    # increased while workers were hammering and every request returned 200.
    local port=$1 cfg=$2
    local rc0 rc1
    rc0=$(reload_count_of "$port")

    local stop_flag="$WORK_DIR/race_stop_$port"
    local fail_file="$WORK_DIR/race_fail_$port"
    rm -f "$stop_flag" "$fail_file"
    local worker_pids=()
    local w
    for w in 1 2 3; do
      (
        local n=0 code
        while [[ ! -f "$stop_flag" ]]; do
          code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$port/api/echo" -d "$CR_BODY")
          if [[ "$code" != "200" ]]; then
            echo "$code" >> "$fail_file"
          fi
          n=$((n + 1))
        done
        echo "$n" > "$WORK_DIR/race_count_${port}_$w"
      ) &
      worker_pids+=($!)
    done

    # Touch repeatedly until THIS server's counter moves. Watcher tick is 2s
    # and C++ mtime granularity is 1s, so a 20s deadline is generous.
    local deadline=$((SECONDS + 20)) reloaded=false
    while (( SECONDS < deadline )); do
      touch "$cfg"
      sleep 0.5
      rc1=$(reload_count_of "$port")
      if (( rc1 > rc0 )); then
        reloaded=true
        break
      fi
    done
    sleep 0.5  # keep the hammer running briefly past the observed reload
    touch "$stop_flag"
    wait "${worker_pids[@]}" 2>/dev/null || true

    local fails=0 reqs=0
    [[ -f "$fail_file" ]] && fails=$(wc -l < "$fail_file")
    for w in 1 2 3; do
      if [[ -f "$WORK_DIR/race_count_${port}_$w" ]]; then
        reqs=$((reqs + $(cat "$WORK_DIR/race_count_${port}_$w")))
      fi
    done

    if $reloaded && [[ $fails -eq 0 && $reqs -gt 0 ]]; then
      return 0
    fi
    echo "        port=$port reloaded=$reloaded reqs=$reqs fails=$fails (reload_count ${rc0} -> ${rc1:-?})" >&2
    return 1
  }

  cr_total=$((cr_total + 1))
  GO_RACE_CONFIG="$WORK_DIR/race_config_go.json"
  JAVA_RACE_CONFIG="$WORK_DIR/race_config_java.json"
  CPP_RACE_CONFIG="$WORK_DIR/race_config_cpp.json"
  cp "$CR_CONFIG" "$GO_RACE_CONFIG"
  cp "$CR_CONFIG" "$JAVA_RACE_CONFIG"
  cp "$CR_CONFIG" "$CPP_RACE_CONFIG"

  "$WORK_DIR/pineapple-server" -config "$GO_RACE_CONFIG" -addr ":$GO_CR_PORT" -demo-routes &
  GO_CR_PID=$!
  java -cp "$JAVA_CP" -Dpine.config="$JAVA_RACE_CONFIG" -Dpine.port=$JAVA_CR_PORT \
    -Dpine.demoRoutes=true page.liam.pine.PineServer &
  JAVA_CR_PID=$!
  if [[ -n "${CPP_SERVER:-}" ]]; then
    "$CPP_SERVER" -config "$CPP_RACE_CONFIG" -addr ":$CPP_CR_PORT" -demo-routes &
    CPP_CR_PID=$!
  fi

  if srv_ready $GO_CR_PORT && srv_ready $JAVA_CR_PORT; then
    go_race_ok=false
    java_race_ok=false
    reload_race_arm $GO_CR_PORT "$GO_RACE_CONFIG" && go_race_ok=true
    reload_race_arm $JAVA_CR_PORT "$JAVA_RACE_CONFIG" && java_race_ok=true
    if $go_race_ok && $java_race_ok; then
      cr_pass=$((cr_pass + 1))
      echo "    [10] custom route stays 200 with a reload completing mid-hammer (Go, Java)"
    else
      fail "custom-routes: reload race (Go ok=$go_race_ok, Java ok=$java_race_ok)"
    fi

    if [[ -n "${CPP_CR_PID:-}" ]]; then
      cpp_cr_total=$((cpp_cr_total + 1))
      if srv_ready $CPP_CR_PORT && reload_race_arm $CPP_CR_PORT "$CPP_RACE_CONFIG"; then
        cpp_cr_pass=$((cpp_cr_pass + 1))
        echo "    [10] C++ custom route stays 200 with a reload completing mid-hammer"
      else
        fail "custom-routes: C++ reload race"
      fi
    fi
  else
    fail "custom-routes: reload-race servers failed to start"
  fi

  cr_cleanup
fi

if [[ $cr_total -gt 0 && $cr_pass -eq $cr_total ]]; then
  pass "custom routes parity ($cr_pass/$cr_total checks)"
elif [[ $cr_total -eq 0 ]]; then
  pass "custom routes parity (skipped)"
fi

if [[ -n "${CPP_SERVER:-}" ]]; then
  # Terminal assertion: with a configured C++ binary the C++ arm must have
  # actually run its checks — zero checks means readiness silently failed
  # (already fail()-ed above, but this pins the invariant explicitly).
  if [[ $cpp_cr_total -gt 0 && $cpp_cr_pass -eq $cpp_cr_total ]]; then
    pass "custom routes parity C++ ($cpp_cr_pass/$cpp_cr_total checks)"
  elif [[ $cpp_cr_total -eq 0 && $cr_total -gt 0 ]]; then
    fail "custom-routes: CPP_SERVER is set but the C++ arm ran zero checks"
  fi
fi
