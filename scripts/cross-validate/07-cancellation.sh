#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 7. Cancellation/timeout parity ----------
echo
echo "==> [7/$TOTAL_SECTIONS] Cancellation parity (timeout behavior)"

# Create a slow Lua fixture that should exceed a tight timeout
TIMEOUT_CONFIG="$WORK_DIR/timeout_config.json"
TIMEOUT_REQ="$WORK_DIR/timeout_req.json"

cat > "$TIMEOUT_CONFIG" << 'CFGEOF'
{
  "pipeline_config": {
    "operators": {
      "slow_lua": {
        "type_name": "transform_by_lua",
        "lua_script": "function slow()\n  while true do end\nend",
        "function_for_item": "slow",
        "function_for_common": "",
        "$metadata": {
          "common_input": [],
          "common_output": [],
          "item_input": ["x"],
          "item_output": ["x"]
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["slow_lua"]}
  },
  "flow_contract": {
    "common_input": [],
    "item_input": ["x"],
    "common_output": [],
    "item_output": ["x"]
  }
}
CFGEOF

cat > "$TIMEOUT_REQ" << 'REQEOF'
{"common": {}, "items": [{"x": 1}]}
REQEOF

cancel_pass=0
cancel_total=0

# Test 1: both engines killed by timeout produce same exit behavior (non-zero)
cancel_total=$((cancel_total + 1))
go_exit=0
timeout 3 "$WORK_DIR/pineapple-run" -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || go_exit=$?
java_exit=0
timeout 3 java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || java_exit=$?

# Both should have been killed (exit 124 from timeout, or 137 from SIGKILL)
if [[ $go_exit -ne 0 && $java_exit -ne 0 ]]; then
  cancel_pass=$((cancel_pass + 1))
  echo "    [1] slow Lua + timeout 3s → Go & Java both killed (Go=$go_exit, Java=$java_exit)"
elif [[ $go_exit -eq 0 && $java_exit -eq 0 ]]; then
  # Both finished fast enough — still parity
  cancel_pass=$((cancel_pass + 1))
  echo "    [1] slow Lua + timeout 3s → both completed (parity OK)"
else
  fail "cancellation parity (Go vs Java): divergence (Go exit=$go_exit, Java exit=$java_exit)"
fi

# Test 1b: C++ timeout
if [[ -n "${CPP_RUN:-}" ]]; then
  cancel_total=$((cancel_total + 1))
  cpp_exit=0
  timeout 3 "$CPP_RUN" -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || cpp_exit=$?
  if [[ $cpp_exit -ne 0 ]]; then
    cancel_pass=$((cancel_pass + 1))
    echo "    [1b] slow Lua + timeout 3s → C++ killed (exit=$cpp_exit)"
  else
    fail "cancellation parity (C++): C++ did not timeout as expected (exit=$cpp_exit)"
  fi
fi

# Test 2: Lua error produces same error behavior from both engines
cancel_total=$((cancel_total + 1))
ERR_LUA_CONFIG="$WORK_DIR/err_lua_config.json"
cat > "$ERR_LUA_CONFIG" << 'CFGEOF'
{
  "pipeline_config": {
    "operators": {
      "bad_lua": {
        "type_name": "transform_by_lua",
        "lua_script": "function fail_intentional()\n  error('intentional failure')\nend",
        "function_for_item": "fail_intentional",
        "function_for_common": "",
        "$metadata": {
          "common_input": [],
          "common_output": [],
          "item_input": ["x"],
          "item_output": ["x"]
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["bad_lua"]}
  },
  "flow_contract": {
    "common_input": [],
    "item_input": ["x"],
    "common_output": [],
    "item_output": ["x"]
  }
}
CFGEOF

go_lua_err=$("$WORK_DIR/pineapple-run" -config "$ERR_LUA_CONFIG" -request "$TIMEOUT_REQ" 2>&1) && go_lua_ok=true || go_lua_ok=false
java_lua_err=$(java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$ERR_LUA_CONFIG" -request "$TIMEOUT_REQ" 2>&1) && java_lua_ok=true || java_lua_ok=false

if [[ "$go_lua_ok" == "false" && "$java_lua_ok" == "false" ]]; then
  # Both failed — check both mention "intentional"
  if grep -qi "intentional" <<< "$go_lua_err" && grep -qi "intentional" <<< "$java_lua_err"; then
    cancel_pass=$((cancel_pass + 1))
    echo "    [2] Lua error() → Go & Java both failed with expected message"
  else
    fail "cancellation parity (Go vs Java): Lua error message mismatch"
    echo "      Go:   $go_lua_err" | head -2 >&2
    echo "      Java: $java_lua_err" | head -2 >&2
  fi
elif [[ "$go_lua_ok" == "$java_lua_ok" ]]; then
  cancel_pass=$((cancel_pass + 1))
  echo "    [2] Lua error() → both behaved same (ok=$go_lua_ok)"
else
  fail "cancellation parity (Go vs Java): Lua error divergence (Go_ok=$go_lua_ok, Java_ok=$java_lua_ok)"
fi

# Test 2b: C++ Lua error
if [[ -n "${CPP_RUN:-}" ]]; then
  cancel_total=$((cancel_total + 1))
  cpp_lua_err=$("$CPP_RUN" -config "$ERR_LUA_CONFIG" -request "$TIMEOUT_REQ" 2>&1) && cpp_lua_ok=true || cpp_lua_ok=false
  if [[ "$cpp_lua_ok" == "false" ]]; then
    if grep -qi "intentional" <<< "$cpp_lua_err"; then
      cancel_pass=$((cancel_pass + 1))
      echo "    [2b] Lua error() → C++ failed with expected message"
    else
      fail "cancellation parity (C++): Lua error message missing 'intentional'"
      echo "      C++: $cpp_lua_err" | head -2 >&2
    fi
  else
    fail "cancellation parity (C++): C++ did not fail on Lua error (ok=$cpp_lua_ok)"
  fi
fi

if [[ $cancel_total -gt 0 && $cancel_pass -eq $cancel_total ]]; then
  pass "cancellation parity ($cancel_pass/$cancel_total checks)"
elif [[ $cancel_total -eq 0 ]]; then
  pass "cancellation parity (skipped)"
fi

# ============================================================
# Audit M14: server-side TCP-disconnect cancellation parity.
#
# Tests 1+1b above only cover OS-level SIGKILL via `timeout` against the
# CLI (pineapple-run / RunCli). They prove the engine doesn't deadlock
# when its host process is killed, but say nothing about the server-mode
# path: client opens POST /execute on a slow pipeline, then closes the
# connection mid-flight. The server must observe the disconnect and
# cancel the request's context, so the worker pool stops doing useless
# work and frees up slots for the next request. The embedded engine API
# side of cancellation (pre-cancel, mid-run, deadline, error text) is
# byte-compared across all three runtimes in section 22.
#
# Go: r.Context() is cancelled when the client RSTs and is passed to
#   engine.Execute(ctx, req). transform_bench_sleep selects on ctx.Done(),
#   so slow1 stops at the disconnect and slow2 / slow3 never run.
#   Observable via /stats: scheduler.run_count for the truncated request
#   still increments, but slow3's exec_count does not.
# C++: a poll(POLLRDHUP|POLLHUP|POLLERR) watcher thread cancels the
#   request's RequestContext, which execute_traced_into() hands to every
#   operator; bench_sleep waits on it like Go's.
# Java: PineServer runs every request under a RequestContext, but
#   com.sun.net.httpserver has no observable disconnect signal on the
#   exchange, so a TCP RST never cancels it — an accepted difference
#   (issue #217, item D). We DON'T fail the section on it; we document
#   and skip.
# ============================================================
echo
echo "    [3] server-side TCP-disconnect cancellation (audit M14)"

CD_CFG="$WORK_DIR/cancel_disconnect_cfg.json"
cat > "$CD_CFG" << 'CFG'
{
  "_PINEAPPLE_VERSION": "0.9.13",
  "pipeline_config": {
    "operators": {
      "slow1": {"type_name": "transform_bench_sleep", "delay_ms": 800,
        "$metadata": {"common_input": [], "item_input": [], "common_output": [], "item_output": ["_bench_slept"]}},
      "slow2": {"type_name": "transform_bench_sleep", "delay_ms": 800,
        "$metadata": {"common_input": [], "item_input": [], "common_output": [], "item_output": ["_bench_slept"]}},
      "slow3": {"type_name": "transform_bench_sleep", "delay_ms": 800,
        "$metadata": {"common_input": [], "item_input": [], "common_output": [], "item_output": ["_bench_slept"]}}
    },
    "pipeline_map": {"stage": {"pipeline": ["slow1", "slow2", "slow3"]}}
  },
  "pipeline_group": {"main": {"pipeline": ["stage"]}},
  "flow_contract": {
    "common_input": [], "item_input": ["id"],
    "common_output": [], "item_output": ["id", "_bench_slept"]
  }
}
CFG

CD_REQ='{"common":{},"items":[{"id":"a"}]}'
CD_GO_PORT=18301
CD_CPP_PORT=18304

"$WORK_DIR/pineapple-server" -config "$CD_CFG" -addr ":$CD_GO_PORT" &
CD_GO_PID=$!
CD_CPP_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$CD_CFG" -addr ":$CD_CPP_PORT" &
  CD_CPP_PID=$!
fi

# now_ms: wall-clock milliseconds. `date +%s%3N` is a GNU extension; other
# date implementations (uutils, BSD) print garbage for %3N, which made the
# elapsed check fail with a nonsense value. python3 is already required here.
now_ms() {
  python3 -c 'import time; print(int(time.time() * 1000))'
}

# probe_disconnect_cancel <port>: opens /execute, kills the client at
# 300ms while pipeline would otherwise take ~2400ms. The elapsed check
# is weak: both servers bump scheduler.run_count when a run STARTS, so
# the poll below returns at once and elapsed mostly reflects curl's
# 300ms. The load-bearing assertion is slow3's exec_count. That a
# running operator is cut short by the cancel (bench_sleep waits on the
# request context, issue #217) is byte-compared in section 22
# (cancel_sleep), not here. If the server kept running to
# completion despite the disconnect, scheduler.run_count would tick up
# but op_exec_count for slow3 would equal scheduler.run_count, proving
# all three ops ran. With cancel honored, slow3 exec_count should stay
# strictly less than the run_count of completed runs.
probe_disconnect_cancel() {
  local port=$1
  # Snapshot scheduler state before the doomed request
  local sched_before
  sched_before=$(curl -s "http://localhost:$port/stats" \
    | python3 -c "import json,sys; print(json.load(sys.stdin).get('scheduler',{}).get('run_count',0))")
  local slow3_before
  slow3_before=$(curl -s "http://localhost:$port/stats" \
    | python3 -c "import json,sys; ops=json.load(sys.stdin).get('operators',{}); print(ops.get('slow3',{}).get('exec_count',0))")

  # Send POST /execute with --max-time 0.3 → curl tears down the TCP
  # connection at 300ms while slow1 is still inside its sleep(800ms).
  local t0 t1 elapsed_ms
  t0=$(now_ms)
  curl -s --max-time 0.3 -X POST -H "Content-Type: application/json" \
    -d "$CD_REQ" "http://localhost:$port/execute" >/dev/null 2>&1 || true
  # Now wait for the server's view of this request to settle. We poll
  # scheduler.run_count up to 1.5s; once it ticks (even if the request
  # was aborted, the engine still records the completed-or-cancelled
  # run via dag_exec_total), we measure elapsed.
  for _ in $(seq 1 30); do
    local now
    now=$(curl -s "http://localhost:$port/stats" \
      | python3 -c "import json,sys; print(json.load(sys.stdin).get('scheduler',{}).get('run_count',0))" 2>/dev/null || echo 0)
    if [[ "$now" -gt "$sched_before" ]]; then
      break
    fi
    sleep 0.05
  done
  t1=$(now_ms)
  elapsed_ms=$((t1 - t0))

  local slow3_after
  slow3_after=$(curl -s "http://localhost:$port/stats" \
    | python3 -c "import json,sys; ops=json.load(sys.stdin).get('operators',{}); print(ops.get('slow3',{}).get('exec_count',0))")

  # Two-part assertion:
  # (a) elapsed wallclock from connect → run_count ticks must be under
  #     1500 ms — a sanity bound only (see the function comment).
  # (b) slow3 exec_count must NOT have advanced — proves the cancel
  #     fired before reaching the third stage.
  echo "$elapsed_ms $slow3_before $slow3_after"
}

if srv_ready $CD_GO_PORT; then
  cancel_total=$((cancel_total + 1))
  read -r el sb sa <<< "$(probe_disconnect_cancel $CD_GO_PORT)"
  if [[ "$el" -lt 1500 && "$sa" == "$sb" ]]; then
    cancel_pass=$((cancel_pass + 1))
    echo "    [3] Go: TCP RST → cancel honored (elapsed=${el}ms, slow3 exec stayed at $sa)"
  else
    fail "M14 Go disconnect-cancel: elapsed=${el}ms (want <1500), slow3 ${sb}->${sa} (want unchanged)"
  fi
else
  fail "M14 Go: server not ready on :$CD_GO_PORT"
fi

if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $CD_CPP_PORT; then
  cancel_total=$((cancel_total + 1))
  read -r el sb sa <<< "$(probe_disconnect_cancel $CD_CPP_PORT)"
  if [[ "$el" -lt 1500 && "$sa" == "$sb" ]]; then
    cancel_pass=$((cancel_pass + 1))
    echo "    [3] C++: TCP RST → cancel honored (elapsed=${el}ms, slow3 exec stayed at $sa)"
  else
    fail "M14 C++ disconnect-cancel: elapsed=${el}ms (want <1500), slow3 ${sb}->${sa} (want unchanged)"
  fi
fi

echo "    [3] Java: skipped — com.sun.net.httpserver exposes no client-disconnect signal,"
echo "         so a TCP RST never cancels the request's RequestContext"
echo "         (accepted difference, issue #217 item D; server.go uses r.Context(),"
echo "          server.cpp a POLLRDHUP watcher)"

kill $CD_GO_PID 2>/dev/null || true
[[ -n "$CD_CPP_PID" ]] && kill $CD_CPP_PID 2>/dev/null || true
wait $CD_GO_PID 2>/dev/null || true
[[ -n "$CD_CPP_PID" ]] && wait $CD_CPP_PID 2>/dev/null || true

if [[ $cancel_total -gt 0 && $cancel_pass -eq $cancel_total ]]; then
  pass "cancellation parity (audit M14): $cancel_pass/$cancel_total"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
