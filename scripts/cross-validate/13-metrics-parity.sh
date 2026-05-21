#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 13. Metrics parity ----------
echo
echo "==> [13/$TOTAL_SECTIONS] Metrics parity (stats endpoint after execution)"

# Use a pipeline that exercises multiple operators including skips
METRICS_FIXTURE="$REPO_ROOT/fixtures/pipelines/skip_branch.json"
METRICS_CONFIG="$WORK_DIR/metrics_config.json"
python3 -c "
import json
with open('$METRICS_FIXTURE') as f:
    data = json.load(f)
with open('$METRICS_CONFIG', 'w') as cf:
    json.dump(data.get('config', {}), cf)
"

METRICS_REQ=$(python3 -c "
import json
with open('$METRICS_FIXTURE') as f:
    data = json.load(f)
print(json.dumps(data['cases'][0]['request']))
")

GO_PORT=18950
JAVA_PORT=18951
PY_PORT=18952
CPP_PORT=18953

"$WORK_DIR/pineapple-server" -config "$METRICS_CONFIG" -addr ":$GO_PORT" &
GO_PID=$!
java -cp "$JAVA_CP" -Dpine.config="$METRICS_CONFIG" -Dpine.port=$JAVA_PORT page.liam.pine.PineServer &
JAVA_PID=$!
(cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.server -config "$METRICS_CONFIG" -addr ":$PY_PORT") &
PY_PID=$!
CPP_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$METRICS_CONFIG" -addr ":$CPP_PORT" &
  CPP_PID=$!
fi

metrics_pass=0
metrics_total=0
cpp_metrics_pass=0
cpp_metrics_total=0
cpp_srv_ready=false

if srv_ready $GO_PORT && srv_ready $JAVA_PORT && srv_ready $PY_PORT; then
  if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $CPP_PORT; then
    cpp_srv_ready=true
  fi
  # ------------------------------------------------------------------
  # [1] Zero-traffic pre-init: all operators visible in /stats immediately
  # This simulates downstream scraping metrics right after engine startup.
  # ------------------------------------------------------------------
  metrics_total=$((metrics_total + 1))
  GO_STATS_COLD=$(curl -s "http://localhost:$GO_PORT/stats")
  JAVA_STATS_COLD=$(curl -s "http://localhost:$JAVA_PORT/stats")
  PY_STATS_COLD=$(curl -s "http://localhost:$PY_PORT/stats")

  preinit_ok=$(python3 -c "
import json, sys
go = json.loads('''$GO_STATS_COLD''')
java = json.loads('''$JAVA_STATS_COLD''')
py = json.loads('''$PY_STATS_COLD''')
go_ops = sorted(go.get('operators', {}).keys())
java_ops = sorted(java.get('operators', {}).keys())
py_ops = sorted(py.get('operators', {}).keys())
if not go_ops:
    print('go: no operators in /stats before first request')
elif go_ops != java_ops:
    print(f'pre-init mismatch: go={go_ops} java={java_ops}')
elif go_ops != py_ops:
    print(f'pre-init mismatch: go={go_ops} py={py_ops}')
else:
    # Verify all counts are zero
    for engine_name, stats in [('go', go), ('java', java), ('py', py)]:
        for op, data in stats.get('operators', {}).items():
            for field in ('exec_count', 'skip_count', 'error_count'):
                if data.get(field, 0) != 0:
                    print(f'{engine_name}/{op}/{field} != 0 before any request')
                    sys.exit(0)
    print('match')
")
  if [[ "$preinit_ok" == "match" ]]; then
    metrics_pass=$((metrics_pass + 1))
    echo "    [1] Zero-traffic pre-init: all operators visible with zero counts"
  else
    fail "metrics: zero-traffic pre-init: $preinit_ok"
  fi

  if $cpp_srv_ready; then
    cpp_metrics_total=$((cpp_metrics_total + 1))
    CPP_STATS_COLD=$(curl -s "http://localhost:$CPP_PORT/stats")
    cpp_preinit_ok=$(python3 -c "
import json
go = json.loads('''$GO_STATS_COLD''')
cpp = json.loads('''$CPP_STATS_COLD''')
go_ops = sorted(go.get('operators', {}).keys())
cpp_ops = sorted(cpp.get('operators', {}).keys())
if go_ops != cpp_ops:
    print(f'pre-init mismatch: go={go_ops} cpp={cpp_ops}')
else:
    for op, data in cpp.get('operators', {}).items():
        for field in ('exec_count', 'skip_count', 'error_count'):
            if data.get(field, 0) != 0:
                print(f'cpp/{op}/{field} != 0 before any request')
                break
        else:
            continue
        break
    else:
        print('match')
")
    if [[ "$cpp_preinit_ok" == "match" ]]; then
      cpp_metrics_pass=$((cpp_metrics_pass + 1))
      echo "    [1] C++ zero-traffic pre-init matches Go"
    else
      fail "metrics: C++ zero-traffic pre-init: $cpp_preinit_ok"
    fi
  fi

  # ------------------------------------------------------------------
  # Send 5 requests to each engine
  # ------------------------------------------------------------------
  for i in $(seq 1 5); do
    curl -s -X POST -H "Content-Type: application/json" -d "$METRICS_REQ" "http://localhost:$GO_PORT/execute" > /dev/null
    curl -s -X POST -H "Content-Type: application/json" -d "$METRICS_REQ" "http://localhost:$JAVA_PORT/execute" > /dev/null
    curl -s -X POST -H "Content-Type: application/json" -d "$METRICS_REQ" "http://localhost:$PY_PORT/execute" > /dev/null
    if $cpp_srv_ready; then
      curl -s -X POST -H "Content-Type: application/json" -d "$METRICS_REQ" "http://localhost:$CPP_PORT/execute" > /dev/null
    fi
  done

  GO_STATS=$(curl -s "http://localhost:$GO_PORT/stats")
  JAVA_STATS=$(curl -s "http://localhost:$JAVA_PORT/stats")
  PY_STATS=$(curl -s "http://localhost:$PY_PORT/stats")
  CPP_STATS=""
  if $cpp_srv_ready; then
    CPP_STATS=$(curl -s "http://localhost:$CPP_PORT/stats")
  fi

  # [2] Operator names must match across all engines
  metrics_total=$((metrics_total + 1))
  op_names_match=$(python3 -c "
import json, sys
go = json.loads('''$GO_STATS''')
java = json.loads('''$JAVA_STATS''')
py = json.loads('''$PY_STATS''')
go_ops = sorted(go.get('operators', {}).keys())
java_ops = sorted(java.get('operators', {}).keys())
py_ops = sorted(py.get('operators', {}).keys())
if go_ops == java_ops == py_ops:
    print('match')
else:
    print(f'go={go_ops} java={java_ops} py={py_ops}')
")
  if [[ "$op_names_match" == "match" ]]; then
    metrics_pass=$((metrics_pass + 1))
    echo "    [2] Operator names match across all engines"
  else
    fail "metrics: operator names differ: $op_names_match"
  fi

  if $cpp_srv_ready; then
    cpp_metrics_total=$((cpp_metrics_total + 1))
    cpp_op_names_match=$(python3 -c "
import json
go = json.loads('''$GO_STATS''')
cpp = json.loads('''$CPP_STATS''')
go_ops = sorted(go.get('operators', {}).keys())
cpp_ops = sorted(cpp.get('operators', {}).keys())
print('match' if go_ops == cpp_ops else f'go={go_ops} cpp={cpp_ops}')
")
    if [[ "$cpp_op_names_match" == "match" ]]; then
      cpp_metrics_pass=$((cpp_metrics_pass + 1))
      echo "    [2] C++ operator names match Go"
    else
      fail "metrics: C++ operator names differ: $cpp_op_names_match"
    fi
  fi

  # [3] exec_count must be 5 for all executed operators in all engines
  metrics_total=$((metrics_total + 1))
  exec_counts_ok=$(python3 -c "
import json, sys
go = json.loads('''$GO_STATS''')
java = json.loads('''$JAVA_STATS''')
py = json.loads('''$PY_STATS''')
def get_exec_counts(stats):
    return {k: v.get('exec_count', 0) for k, v in stats.get('operators', {}).items()}
go_ec = get_exec_counts(go)
java_ec = get_exec_counts(java)
py_ec = get_exec_counts(py)
if go_ec == java_ec == py_ec:
    print('match')
else:
    print(f'go={go_ec} java={java_ec} py={py_ec}')
")
  if [[ "$exec_counts_ok" == "match" ]]; then
    metrics_pass=$((metrics_pass + 1))
    echo "    [3] exec_count matches across all engines"
  else
    fail "metrics: exec_count differs: $exec_counts_ok"
  fi

  if $cpp_srv_ready; then
    cpp_metrics_total=$((cpp_metrics_total + 1))
    cpp_exec_counts_ok=$(python3 -c "
import json
go = json.loads('''$GO_STATS''')
cpp = json.loads('''$CPP_STATS''')
def get_exec_counts(stats):
    return {k: v.get('exec_count', 0) for k, v in stats.get('operators', {}).items()}
go_ec = get_exec_counts(go)
cpp_ec = get_exec_counts(cpp)
print('match' if go_ec == cpp_ec else f'go={go_ec} cpp={cpp_ec}')
")
    if [[ "$cpp_exec_counts_ok" == "match" ]]; then
      cpp_metrics_pass=$((cpp_metrics_pass + 1))
      echo "    [3] C++ exec_count matches Go"
    else
      fail "metrics: C++ exec_count differs: $cpp_exec_counts_ok"
    fi
  fi

  # [4] skip_count must match across all engines
  metrics_total=$((metrics_total + 1))
  skip_counts_ok=$(python3 -c "
import json, sys
go = json.loads('''$GO_STATS''')
java = json.loads('''$JAVA_STATS''')
py = json.loads('''$PY_STATS''')
def get_skip_counts(stats):
    return {k: v.get('skip_count', 0) for k, v in stats.get('operators', {}).items()}
go_sc = get_skip_counts(go)
java_sc = get_skip_counts(java)
py_sc = get_skip_counts(py)
if go_sc == java_sc == py_sc:
    print('match')
else:
    print(f'go={go_sc} java={java_sc} py={py_sc}')
")
  if [[ "$skip_counts_ok" == "match" ]]; then
    metrics_pass=$((metrics_pass + 1))
    echo "    [4] skip_count matches across all engines"
  else
    fail "metrics: skip_count differs: $skip_counts_ok"
  fi

  if $cpp_srv_ready; then
    cpp_metrics_total=$((cpp_metrics_total + 1))
    cpp_skip_counts_ok=$(python3 -c "
import json
go = json.loads('''$GO_STATS''')
cpp = json.loads('''$CPP_STATS''')
def get_skip_counts(stats):
    return {k: v.get('skip_count', 0) for k, v in stats.get('operators', {}).items()}
go_sc = get_skip_counts(go)
cpp_sc = get_skip_counts(cpp)
print('match' if go_sc == cpp_sc else f'go={go_sc} cpp={cpp_sc}')
")
    if [[ "$cpp_skip_counts_ok" == "match" ]]; then
      cpp_metrics_pass=$((cpp_metrics_pass + 1))
      echo "    [4] C++ skip_count matches Go"
    else
      fail "metrics: C++ skip_count differs: $cpp_skip_counts_ok"
    fi
  fi

  # [5] error_count must match (should be 0 for normal requests)
  metrics_total=$((metrics_total + 1))
  error_counts_ok=$(python3 -c "
import json, sys
go = json.loads('''$GO_STATS''')
java = json.loads('''$JAVA_STATS''')
py = json.loads('''$PY_STATS''')
def get_error_counts(stats):
    return {k: v.get('error_count', 0) for k, v in stats.get('operators', {}).items()}
go_errc = get_error_counts(go)
java_errc = get_error_counts(java)
py_errc = get_error_counts(py)
if go_errc == java_errc == py_errc:
    print('match')
else:
    print(f'go={go_errc} java={java_errc} py={py_errc}')
")
  if [[ "$error_counts_ok" == "match" ]]; then
    metrics_pass=$((metrics_pass + 1))
    echo "    [5] error_count matches across all engines"
  else
    fail "metrics: error_count differs: $error_counts_ok"
  fi

  if $cpp_srv_ready; then
    cpp_metrics_total=$((cpp_metrics_total + 1))
    cpp_error_counts_ok=$(python3 -c "
import json
go = json.loads('''$GO_STATS''')
cpp = json.loads('''$CPP_STATS''')
def get_error_counts(stats):
    return {k: v.get('error_count', 0) for k, v in stats.get('operators', {}).items()}
go_errc = get_error_counts(go)
cpp_errc = get_error_counts(cpp)
print('match' if go_errc == cpp_errc else f'go={go_errc} cpp={cpp_errc}')
")
    if [[ "$cpp_error_counts_ok" == "match" ]]; then
      cpp_metrics_pass=$((cpp_metrics_pass + 1))
      echo "    [5] C++ error_count matches Go"
    else
      fail "metrics: C++ error_count differs: $cpp_error_counts_ok"
    fi
  fi

  # [6] scheduler.run_count must be 5 for all engines
  metrics_total=$((metrics_total + 1))
  total_exec_ok=$(python3 -c "
import json, sys
go = json.loads('''$GO_STATS''')
java = json.loads('''$JAVA_STATS''')
py = json.loads('''$PY_STATS''')
go_te = go.get('scheduler', {}).get('run_count', 0)
java_te = java.get('scheduler', {}).get('run_count', 0)
py_te = py.get('scheduler', {}).get('run_count', 0)
if go_te == java_te == py_te == 5:
    print('match')
else:
    print(f'go={go_te} java={java_te} py={py_te}')
")
  if [[ "$total_exec_ok" == "match" ]]; then
    metrics_pass=$((metrics_pass + 1))
    echo "    [6] scheduler.run_count = 5 for all engines"
  else
    fail "metrics: scheduler.run_count differs: $total_exec_ok"
  fi

  if $cpp_srv_ready; then
    cpp_metrics_total=$((cpp_metrics_total + 1))
    cpp_run_count=$(python3 -c "
import json
cpp = json.loads('''$CPP_STATS''')
print(cpp.get('scheduler', {}).get('run_count', 0))
")
    if [[ "$cpp_run_count" == "5" ]]; then
      cpp_metrics_pass=$((cpp_metrics_pass + 1))
      echo "    [6] C++ scheduler.run_count = 5"
    else
      fail "metrics: C++ scheduler.run_count = $cpp_run_count"
    fi
  fi

  kill $GO_PID $JAVA_PID $PY_PID 2>/dev/null || true
  [[ -n "$CPP_PID" ]] && kill $CPP_PID 2>/dev/null || true
  wait $GO_PID $JAVA_PID $PY_PID 2>/dev/null || true
  [[ -n "$CPP_PID" ]] && wait $CPP_PID 2>/dev/null || true
  GO_PID=""
  JAVA_PID=""
  PY_PID=""
  CPP_PID=""
else
  fail "metrics: servers failed to start"
  kill $GO_PID $JAVA_PID $PY_PID 2>/dev/null || true
  [[ -n "$CPP_PID" ]] && kill $CPP_PID 2>/dev/null || true
  wait $GO_PID $JAVA_PID $PY_PID 2>/dev/null || true
  [[ -n "$CPP_PID" ]] && wait $CPP_PID 2>/dev/null || true
  GO_PID=""
  JAVA_PID=""
  PY_PID=""
  CPP_PID=""
fi

if [[ $metrics_total -gt 0 && $metrics_pass -eq $metrics_total ]]; then
  pass "metrics parity ($metrics_pass/$metrics_total checks)"
elif [[ $metrics_total -eq 0 ]]; then
  pass "metrics parity (skipped)"
fi

if [[ -n "${CPP_SERVER:-}" ]]; then
  if [[ $cpp_metrics_total -gt 0 && $cpp_metrics_pass -eq $cpp_metrics_total ]]; then
    pass "metrics parity Go vs C++ ($cpp_metrics_pass/$cpp_metrics_total checks)"
  elif [[ $cpp_metrics_total -eq 0 ]]; then
    pass "metrics parity Go vs C++ (skipped)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
