#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 6. Server HTTP parity ----------
echo
echo "==> [6/$TOTAL_SECTIONS] Server HTTP parity (Go vs Java vs C++ endpoint behavior)"

# Pick a simple fixture for server testing
SRV_FIXTURE="$REPO_ROOT/fixtures/pipelines/transform_then_filter.json"
SRV_CONFIG="$WORK_DIR/srv_config.json"
python3 -c "
import json
with open('$SRV_FIXTURE') as f:
    data = json.load(f)
cfg = data.get('config', {})
with open('$SRV_CONFIG', 'w') as cf:
    json.dump(cfg, cf)
"

GO_PORT=18001
JAVA_PORT=18002
CPP_PORT=18004

# Start Go server
"$WORK_DIR/pineapple-server" -config "$SRV_CONFIG" -addr ":$GO_PORT" &
GO_SRV_PID=$!

# Start Java server
java -cp "$JAVA_CP" -Dpine.config="$SRV_CONFIG" -Dpine.port=$JAVA_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

# Start C++ server (conditional)
CPP_SRV_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$SRV_CONFIG" -addr ":$CPP_PORT" 2>/dev/null &
  CPP_SRV_PID=$!
fi

srv_cleanup() {
  [[ -n "${GO_SRV_PID:-}" ]] && kill $GO_SRV_PID 2>/dev/null || true
  [[ -n "${JAVA_SRV_PID:-}" ]] && kill $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "${CPP_SRV_PID:-}" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID 2>/dev/null || true
  wait $JAVA_SRV_PID 2>/dev/null || true
  wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  CPP_SRV_PID=""
}
trap 'srv_cleanup' EXIT

srv_pass=0
srv_total=0
cpp_srv_pass=0
cpp_srv_total=0

cpp_srv_ready=false
if [[ -n "${CPP_SERVER:-}" ]]; then
  if srv_ready $CPP_PORT; then
    cpp_srv_ready=true
  else
    echo "    C++ server failed to start, skipping C++ comparisons"
  fi
fi

if ! srv_ready $GO_PORT; then
  fail "server HTTP: Go server failed to start"
  srv_cleanup
elif ! srv_ready $JAVA_PORT; then
  fail "server HTTP: Java server failed to start"
  srv_cleanup
else
  echo "    All servers ready."

  # Test 1: GET /health
  srv_total=$((srv_total + 1))
  go_health=$(curl -s "http://localhost:$GO_PORT/health")
  java_health=$(curl -s "http://localhost:$JAVA_PORT/health")
  if [[ "$go_health" == "$java_health" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [1] GET /health Go vs Java → match"
  else
    fail "server HTTP: /health divergence (Go vs Java)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_health=$(curl -s "http://localhost:$CPP_PORT/health")
    if [[ "$go_health" == "$cpp_health" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [1] GET /health Go vs C++ → match"
    else
      fail "server HTTP: /health divergence (Go vs C++)"
    fi
  fi

  # Test 2: POST /execute with valid request
  srv_total=$((srv_total + 1))
  SRV_REQ=$(python3 -c "
import json
with open('$SRV_FIXTURE') as f:
    data = json.load(f)
req = data['cases'][0]['request']
print(json.dumps(req))
")
  go_exec=$(curl -s -X POST -H "Content-Type: application/json" -d "$SRV_REQ" "http://localhost:$GO_PORT/execute" | normalize_json)
  java_exec=$(curl -s -X POST -H "Content-Type: application/json" -d "$SRV_REQ" "http://localhost:$JAVA_PORT/execute" | normalize_json)
  if [[ "$go_exec" == "$java_exec" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [2] POST /execute (valid) Go vs Java → match"
  else
    fail "server HTTP: /execute valid request divergence (Go vs Java)"
    diff <(echo "$go_exec" | python3 -m json.tool) <(echo "$java_exec" | python3 -m json.tool) >&2 || true
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_exec=$(curl -s -X POST -H "Content-Type: application/json" -d "$SRV_REQ" "http://localhost:$CPP_PORT/execute" | normalize_json)
    if [[ "$go_exec" == "$cpp_exec" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [2] POST /execute (valid) Go vs C++ → match"
    else
      fail "server HTTP: /execute valid request divergence (Go vs C++)"
      diff <(echo "$go_exec" | python3 -m json.tool) <(echo "$cpp_exec" | python3 -m json.tool) >&2 || true
    fi
  fi

  # Test 3: GET /execute (wrong method) → 405
  srv_total=$((srv_total + 1))
  go_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_PORT/execute")
  java_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_PORT/execute")
  if [[ "$go_405_code" == "$java_405_code" ]] && [[ "$go_405_code" == "405" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [3] GET /execute → 405 Go vs Java match"
  else
    fail "server HTTP: /execute wrong method (Go=$go_405_code, Java=$java_405_code)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$CPP_PORT/execute")
    if [[ "$go_405_code" == "$cpp_405_code" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [3] GET /execute → 405 Go vs C++ match"
    else
      fail "server HTTP: /execute wrong method (Go=$go_405_code, C++=$cpp_405_code)"
    fi
  fi

  # Test 4: POST /execute with invalid JSON → 400
  srv_total=$((srv_total + 1))
  go_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$GO_PORT/execute")
  java_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$JAVA_PORT/execute")
  if [[ "$go_400_code" == "$java_400_code" ]] && [[ "$go_400_code" == "400" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [4] POST /execute (bad JSON) → 400 Go vs Java match"
  else
    fail "server HTTP: /execute bad JSON (Go=$go_400_code, Java=$java_400_code)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$CPP_PORT/execute")
    if [[ "$go_400_code" == "$cpp_400_code" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [4] POST /execute (bad JSON) → 400 Go vs C++ match"
    else
      fail "server HTTP: /execute bad JSON (Go=$go_400_code, C++=$cpp_400_code)"
    fi
  fi

  # Test 5: GET /dag → DOT output parity
  srv_total=$((srv_total + 1))
  go_dag=$(curl -s "http://localhost:$GO_PORT/dag")
  java_dag=$(curl -s "http://localhost:$JAVA_PORT/dag")
  if [[ "$go_dag" == "$java_dag" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [5] GET /dag Go vs Java → match"
  else
    fail "server HTTP: /dag divergence (Go vs Java)"
    diff <(echo "$go_dag") <(echo "$java_dag") >&2 || true
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_dag=$(curl -s "http://localhost:$CPP_PORT/dag")
    if [[ "$go_dag" == "$cpp_dag" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [5] GET /dag Go vs C++ → match"
    else
      fail "server HTTP: /dag divergence (Go vs C++)"
      diff <(echo "$go_dag") <(echo "$cpp_dag") >&2 || true
    fi
  fi

  # Test 6: GET /stats → structure parity (compare after execute)
  srv_total=$((srv_total + 1))
  go_stats_keys=$(curl -s "http://localhost:$GO_PORT/stats" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  java_stats_keys=$(curl -s "http://localhost:$JAVA_PORT/stats" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  if [[ "$go_stats_keys" == "$java_stats_keys" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [6] GET /stats → top-level keys Go vs Java match"
  else
    fail "server HTTP: /stats keys divergence (Go=$go_stats_keys, Java=$java_stats_keys)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_stats_keys=$(curl -s "http://localhost:$CPP_PORT/stats" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
    if [[ "$go_stats_keys" == "$cpp_stats_keys" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [6] GET /stats → top-level keys Go vs C++ match"
    else
      fail "server HTTP: /stats keys divergence (Go=$go_stats_keys, C++=$cpp_stats_keys)"
    fi
  fi

  # Test 7: GET /stats → operator sub-structure key parity
  srv_total=$((srv_total + 1))
  go_op_keys=$(curl -s "http://localhost:$GO_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ops = d.get('operators', {})
if ops:
    first = next(iter(ops.values()))
    print(sorted(first.keys()))
else:
    print('[]')
")
  java_op_keys=$(curl -s "http://localhost:$JAVA_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ops = d.get('operators', {})
if ops:
    first = next(iter(ops.values()))
    print(sorted(first.keys()))
else:
    print('[]')
")
  if [[ "$go_op_keys" == "$java_op_keys" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [7] GET /stats → operator stat keys Go vs Java match"
  else
    fail "server HTTP: /stats operator keys divergence (Go=$go_op_keys, Java=$java_op_keys)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_op_keys=$(curl -s "http://localhost:$CPP_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ops = d.get('operators', {})
if ops:
    first = next(iter(ops.values()))
    print(sorted(first.keys()))
else:
    print('[]')
")
    if [[ "$go_op_keys" == "$cpp_op_keys" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [7] GET /stats → operator stat keys Go vs C++ match"
    else
      fail "server HTTP: /stats operator keys divergence (Go=$go_op_keys, C++=$cpp_op_keys)"
    fi
  fi

  # Test 7b: GET /stats → operator name ordering parity (JSON key order)
  srv_total=$((srv_total + 1))
  go_op_names=$(curl -s "http://localhost:$GO_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(list(d.get('operators', {}).keys()))
")
  java_op_names=$(curl -s "http://localhost:$JAVA_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(list(d.get('operators', {}).keys()))
")
  if [[ "$go_op_names" == "$java_op_names" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [7b] GET /stats → operator name ordering Go vs Java match"
  else
    fail "server HTTP: /stats operator ordering (Go=$go_op_names, Java=$java_op_names)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_op_names=$(curl -s "http://localhost:$CPP_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
print(list(d.get('operators', {}).keys()))
")
    if [[ "$go_op_names" == "$cpp_op_names" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [7b] GET /stats → operator name ordering Go vs C++ match"
    else
      fail "server HTTP: /stats operator ordering (Go=$go_op_names, C++=$cpp_op_names)"
    fi
  fi

  # Test 8: POST /execute (bad JSON) → verify 400 body contains "error" field
  srv_total=$((srv_total + 1))
  go_400_body=$(curl -s -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$GO_PORT/execute")
  java_400_body=$(curl -s -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$JAVA_PORT/execute")
  go_400_has_error=$(echo "$go_400_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('error' in d)")
  java_400_has_error=$(echo "$java_400_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('error' in d)")
  if [[ "$go_400_has_error" == "True" && "$java_400_has_error" == "True" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [8] POST /execute (bad JSON) → 400 body has error field (Go vs Java)"
  else
    fail "server HTTP: /execute 400 body structure (Go=$go_400_has_error, Java=$java_400_has_error)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_400_body=$(curl -s -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$CPP_PORT/execute")
    cpp_400_has_error=$(echo "$cpp_400_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('error' in d)")
    if [[ "$go_400_has_error" == "True" && "$cpp_400_has_error" == "True" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [8] POST /execute (bad JSON) → 400 body has error field (Go vs C++)"
    else
      fail "server HTTP: /execute 400 body structure (Go=$go_400_has_error, C++=$cpp_400_has_error)"
    fi
  fi

  # Test 9: POST /execute (missing required field) → 400 ValidationError
  srv_total=$((srv_total + 1))
  go_val_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$GO_PORT/execute")
  java_val_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$JAVA_PORT/execute")
  if [[ "$go_val_code" == "$java_val_code" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [9] POST /execute (missing field) → $go_val_code Go vs Java match"
  else
    fail "server HTTP: ValidationError status divergence (Go=$go_val_code, Java=$java_val_code)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_val_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$CPP_PORT/execute")
    if [[ "$go_val_code" == "$cpp_val_code" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [9] POST /execute (missing field) → $go_val_code Go vs C++ match"
    else
      fail "server HTTP: ValidationError status divergence (Go=$go_val_code, C++=$cpp_val_code)"
    fi
  fi

  # Test 10: POST /execute with _return_trace → trace structure parity
  srv_total=$((srv_total + 1))
  TRACE_REQ=$(python3 -c "
import json
with open('$SRV_FIXTURE') as f:
    data = json.load(f)
req = data['cases'][0]['request']
req['common']['_return_trace'] = True
print(json.dumps(req))
")
  go_trace_body=$(curl -s -X POST -H "Content-Type: application/json" -d "$TRACE_REQ" "http://localhost:$GO_PORT/execute")
  java_trace_body=$(curl -s -X POST -H "Content-Type: application/json" -d "$TRACE_REQ" "http://localhost:$JAVA_PORT/execute")
  go_trace_struct=$(echo "$go_trace_body" | python3 -c "
import json, sys
d = json.load(sys.stdin)
trace = d.get('trace', [])
if trace:
    keys = sorted(trace[0].keys())
    print(f'count={len(trace)} keys={keys}')
else:
    print('no_trace')
")
  java_trace_struct=$(echo "$java_trace_body" | python3 -c "
import json, sys
d = json.load(sys.stdin)
trace = d.get('trace', [])
if trace:
    keys = sorted(trace[0].keys())
    print(f'count={len(trace)} keys={keys}')
else:
    print('no_trace')
")
  if [[ "$go_trace_struct" == "$java_trace_struct" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [10] POST /execute (_return_trace) → trace structure Go vs Java match ($go_trace_struct)"
  else
    fail "server HTTP: _return_trace structure divergence (Go=$go_trace_struct, Java=$java_trace_struct)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_trace_body=$(curl -s -X POST -H "Content-Type: application/json" -d "$TRACE_REQ" "http://localhost:$CPP_PORT/execute")
    cpp_trace_struct=$(echo "$cpp_trace_body" | python3 -c "
import json, sys
d = json.load(sys.stdin)
trace = d.get('trace', [])
if trace:
    keys = sorted(trace[0].keys())
    print(f'count={len(trace)} keys={keys}')
else:
    print('no_trace')
")
    if [[ "$go_trace_struct" == "$cpp_trace_struct" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [10] POST /execute (_return_trace) → trace structure Go vs C++ match"
    else
      fail "server HTTP: _return_trace structure divergence (Go=$go_trace_struct, C++=$cpp_trace_struct)"
    fi
  fi

  # Test 11: POST /execute with oversized body → 413
  srv_total=$((srv_total + 1))
  python3 -c "
import sys
# Generate ~11MB payload (exceeds 10MB default limit)
items = ','.join(['{\"x\":\"' + 'A'*1000 + '\"}'] * 11000)
sys.stdout.write('{\"common\":{},\"items\":[' + items + ']}')
" > "$WORK_DIR/large_body.json"
  go_413_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" --data-binary "@$WORK_DIR/large_body.json" "http://localhost:$GO_PORT/execute" 2>/dev/null || true)
  java_413_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" --data-binary "@$WORK_DIR/large_body.json" "http://localhost:$JAVA_PORT/execute" 2>/dev/null || true)
  if [[ "$go_413_code" == "$java_413_code" ]] && [[ "$go_413_code" == "413" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [11] POST /execute (oversized body) → 413 Go vs Java match"
  else
    fail "server HTTP: oversized body (Go=$go_413_code, Java=$java_413_code)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_413_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" --data-binary "@$WORK_DIR/large_body.json" "http://localhost:$CPP_PORT/execute" 2>/dev/null || true)
    if [[ "$go_413_code" == "$cpp_413_code" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [11] POST /execute (oversized body) → 413 Go vs C++ match"
    else
      fail "server HTTP: oversized body (Go=$go_413_code, C++=$cpp_413_code)"
    fi
  fi

  # Test 12: GET /dag?format=mermaid → Mermaid output parity
  srv_total=$((srv_total + 1))
  go_dag_mmd=$(curl -s "http://localhost:$GO_PORT/dag?format=mermaid")
  java_dag_mmd=$(curl -s "http://localhost:$JAVA_PORT/dag?format=mermaid")
  if [[ "$go_dag_mmd" == "$java_dag_mmd" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [12] GET /dag?format=mermaid Go vs Java → match"
  else
    fail "server HTTP: /dag?format=mermaid divergence (Go vs Java)"
    diff <(echo "$go_dag_mmd") <(echo "$java_dag_mmd") >&2 || true
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_dag_mmd=$(curl -s "http://localhost:$CPP_PORT/dag?format=mermaid")
    if [[ "$go_dag_mmd" == "$cpp_dag_mmd" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [12] GET /dag?format=mermaid Go vs C++ → match"
    else
      fail "server HTTP: /dag?format=mermaid divergence (Go vs C++)"
      diff <(echo "$go_dag_mmd") <(echo "$cpp_dag_mmd") >&2 || true
    fi
  fi

  # Test 12b: GET /dag?collapse=1 → collapsed DAG via HTTP endpoint
  srv_total=$((srv_total + 1))
  go_dag_col=$(curl -s "http://localhost:$GO_PORT/dag?collapse=1")
  java_dag_col=$(curl -s "http://localhost:$JAVA_PORT/dag?collapse=1")
  if [[ "$go_dag_col" == "$java_dag_col" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [12b] GET /dag?collapse=1 Go vs Java → match"
  else
    fail "server HTTP: /dag?collapse=1 divergence (Go vs Java)"
    diff <(echo "$go_dag_col") <(echo "$java_dag_col") >&2 || true
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_dag_col=$(curl -s "http://localhost:$CPP_PORT/dag?collapse=1")
    if [[ "$go_dag_col" == "$cpp_dag_col" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [12b] GET /dag?collapse=1 Go vs C++ → match"
    else
      fail "server HTTP: /dag?collapse=1 divergence (Go vs C++)"
      diff <(echo "$go_dag_col") <(echo "$cpp_dag_col") >&2 || true
    fi
  fi

  # Test 13: GET /dag?format=invalid → error response parity
  srv_total=$((srv_total + 1))
  go_dag_inv_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_PORT/dag?format=invalid")
  java_dag_inv_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_PORT/dag?format=invalid")
  go_dag_inv_body=$(curl -s "http://localhost:$GO_PORT/dag?format=invalid" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))" 2>/dev/null || echo "non-json")
  java_dag_inv_body=$(curl -s "http://localhost:$JAVA_PORT/dag?format=invalid" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))" 2>/dev/null || echo "non-json")
  if [[ "$go_dag_inv_code" == "$java_dag_inv_code" && "$go_dag_inv_body" == "$java_dag_inv_body" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [13] GET /dag?format=invalid → $go_dag_inv_code + body keys Go vs Java match"
  else
    fail "server HTTP: /dag?format=invalid divergence (Go=$go_dag_inv_code/$go_dag_inv_body, Java=$java_dag_inv_code/$java_dag_inv_body)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_dag_inv_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$CPP_PORT/dag?format=invalid")
    cpp_dag_inv_body=$(curl -s "http://localhost:$CPP_PORT/dag?format=invalid" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))" 2>/dev/null || echo "non-json")
    if [[ "$go_dag_inv_code" == "$cpp_dag_inv_code" && "$go_dag_inv_body" == "$cpp_dag_inv_body" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [13] GET /dag?format=invalid → $go_dag_inv_code + body keys Go vs C++ match"
    else
      fail "server HTTP: /dag?format=invalid divergence (Go=$go_dag_inv_code/$go_dag_inv_body, C++=$cpp_dag_inv_code/$cpp_dag_inv_body)"
    fi
  fi

  # Test 14: POST /execute (missing field) → validation error body keys parity
  srv_total=$((srv_total + 1))
  go_val_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$GO_PORT/execute")
  java_val_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$JAVA_PORT/execute")
  go_val_keys=$(echo "$go_val_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  java_val_keys=$(echo "$java_val_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  if [[ "$go_val_keys" == "$java_val_keys" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [14] POST /execute (validation error) → body keys Go vs Java match ($go_val_keys)"
  else
    fail "server HTTP: validation error body keys (Go=$go_val_keys, Java=$java_val_keys)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_val_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$CPP_PORT/execute")
    cpp_val_keys=$(echo "$cpp_val_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
    if [[ "$go_val_keys" == "$cpp_val_keys" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [14] POST /execute (validation error) → body keys Go vs C++ match"
    else
      fail "server HTTP: validation error body keys (Go=$go_val_keys, C++=$cpp_val_keys)"
    fi
  fi

  # Test 15: Content-Type header parity across endpoints
  srv_total=$((srv_total + 1))
  ct_java_pass=true
  for ep in "/health" "/stats" "/dag"; do
    go_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$GO_PORT$ep")
    java_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$JAVA_PORT$ep")
    if [[ "$go_ct" != "$java_ct" ]]; then
      ct_java_pass=false
      fail "server HTTP: Content-Type mismatch for $ep (Go='$go_ct', Java='$java_ct')"
      break
    fi
  done
  go_ct=$(curl -s -o /dev/null -w "%{content_type}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$GO_PORT/execute")
  java_ct=$(curl -s -o /dev/null -w "%{content_type}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$JAVA_PORT/execute")
  if [[ "$go_ct" != "$java_ct" ]]; then
    ct_java_pass=false
    fail "server HTTP: Content-Type mismatch for /execute (Go='$go_ct', Java='$java_ct')"
  fi
  if $ct_java_pass; then
    srv_pass=$((srv_pass + 1))
    echo "    [15] Content-Type headers → Go vs Java match across all endpoints"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    ct_cpp_pass=true
    for ep in "/health" "/stats" "/dag"; do
      go_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$GO_PORT$ep")
      cpp_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$CPP_PORT$ep")
      if [[ "$go_ct" != "$cpp_ct" ]]; then
        ct_cpp_pass=false
        fail "server HTTP: Content-Type mismatch for $ep (Go='$go_ct', C++='$cpp_ct')"
        break
      fi
    done
    if $ct_cpp_pass; then
      go_ct=$(curl -s -o /dev/null -w "%{content_type}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$GO_PORT/execute")
      cpp_ct=$(curl -s -o /dev/null -w "%{content_type}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$CPP_PORT/execute")
      if [[ "$go_ct" != "$cpp_ct" ]]; then
        ct_cpp_pass=false
        fail "server HTTP: Content-Type mismatch for /execute (Go='$go_ct', C++='$cpp_ct')"
      fi
    fi
    if $ct_cpp_pass; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [15] Content-Type headers → Go vs C++ match across all endpoints"
    fi
  fi

  # Test 15b: POST /health → 405 method not allowed (all sides)
  srv_total=$((srv_total + 1))
  go_health_post=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$GO_PORT/health")
  java_health_post=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$JAVA_PORT/health")
  if [[ "$go_health_post" == "$java_health_post" && "$go_health_post" == "405" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [15b] POST /health → 405 Go vs Java match"
  else
    fail "server HTTP: POST /health method check (Go=$go_health_post, Java=$java_health_post)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_health_post=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$CPP_PORT/health")
    if [[ "$go_health_post" == "$cpp_health_post" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [15b] POST /health → 405 Go vs C++ match"
    else
      fail "server HTTP: POST /health method check (Go=$go_health_post, C++=$cpp_health_post)"
    fi
  fi

  # Test 15c: POST /execute without "common" key → 400 + error message parity
  srv_total=$((srv_total + 1))
  go_nocommon_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"items":[{"x":1}]}' "http://localhost:$GO_PORT/execute")
  java_nocommon_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"items":[{"x":1}]}' "http://localhost:$JAVA_PORT/execute")
  go_nocommon_msg=$(curl -s -X POST -H "Content-Type: application/json" -d '{"items":[{"x":1}]}' "http://localhost:$GO_PORT/execute" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',''))" 2>/dev/null)
  java_nocommon_msg=$(curl -s -X POST -H "Content-Type: application/json" -d '{"items":[{"x":1}]}' "http://localhost:$JAVA_PORT/execute" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',''))" 2>/dev/null)
  if [[ "$go_nocommon_code" == "$java_nocommon_code" && "$go_nocommon_msg" == "$java_nocommon_msg" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [15c] POST /execute (no common) → $go_nocommon_code + error Go vs Java match"
  else
    fail "server HTTP: missing common (Go=$go_nocommon_code/'$go_nocommon_msg', Java=$java_nocommon_code/'$java_nocommon_msg')"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_nocommon_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"items":[{"x":1}]}' "http://localhost:$CPP_PORT/execute")
    cpp_nocommon_msg=$(curl -s -X POST -H "Content-Type: application/json" -d '{"items":[{"x":1}]}' "http://localhost:$CPP_PORT/execute" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d.get('error',''))" 2>/dev/null)
    if [[ "$go_nocommon_code" == "$cpp_nocommon_code" && "$go_nocommon_msg" == "$cpp_nocommon_msg" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [15c] POST /execute (no common) → $go_nocommon_code + error Go vs C++ match"
    else
      fail "server HTTP: missing common (Go=$go_nocommon_code/'$go_nocommon_msg', C++=$cpp_nocommon_code/'$cpp_nocommon_msg')"
    fi
  fi

  srv_cleanup
fi

# Second server trio: test 500 partial result body (Lua error config)
SRV_ERR_CONFIG="$WORK_DIR/srv_err_config.json"
cat > "$SRV_ERR_CONFIG" << 'CFGEOF'
{
  "pipeline_config": {
    "operators": {
      "bad_lua": {
        "type_name": "transform_by_lua",
        "lua_script": "function fail_now()\n  error('intentional server failure')\nend",
        "function_for_item": "fail_now",
        "function_for_common": "",
        "$metadata": {
          "common_input": [], "common_output": [],
          "item_input": ["x"], "item_output": ["y"]
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["bad_lua"]}
  },
  "flow_contract": {
    "common_input": [], "item_input": ["x"],
    "common_output": [], "item_output": ["x", "y"]
  }
}
CFGEOF

GO_ERR_PORT=18005
JAVA_ERR_PORT=18006
CPP_ERR_PORT=18008

"$WORK_DIR/pineapple-server" -config "$SRV_ERR_CONFIG" -addr ":$GO_ERR_PORT" &
GO_SRV_PID=$!

java -cp "$JAVA_CP" -Dpine.config="$SRV_ERR_CONFIG" -Dpine.port=$JAVA_ERR_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

CPP_SRV_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$SRV_ERR_CONFIG" -addr ":$CPP_ERR_PORT" 2>/dev/null &
  CPP_SRV_PID=$!
fi

cpp_err_ready=false
if [[ -n "${CPP_SERVER:-}" ]]; then
  if srv_ready $CPP_ERR_PORT; then
    cpp_err_ready=true
  fi
fi

if srv_ready $GO_ERR_PORT && srv_ready $JAVA_ERR_PORT; then
  # Test 16: POST /execute (runtime error) → 500 + error field + body structure
  srv_total=$((srv_total + 1))
  go_500_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$GO_ERR_PORT/execute")
  java_500_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$JAVA_ERR_PORT/execute")
  go_500_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$GO_ERR_PORT/execute")
  java_500_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$JAVA_ERR_PORT/execute")

  go_500_keys=$(echo "$go_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  java_500_keys=$(echo "$java_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")

  if [[ "$go_500_code" == "500" && "$java_500_code" == "500" && "$go_500_keys" == "$java_500_keys" ]]; then
    go_has_err=$(echo "$go_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('intentional' in d.get('error',''))")
    java_has_err=$(echo "$java_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('intentional' in d.get('error',''))")
    if [[ "$go_has_err" == "True" && "$java_has_err" == "True" ]]; then
      srv_pass=$((srv_pass + 1))
      echo "    [16] POST /execute (runtime error) → 500 + body keys + error contains 'intentional' (Go vs Java)"
    else
      fail "server HTTP: 500 error message mismatch (Go=$go_has_err, Java=$java_has_err)"
    fi
  else
    fail "server HTTP: 500 response divergence (Go=$go_500_code keys=$go_500_keys, Java=$java_500_code keys=$java_500_keys)"
  fi

  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_err_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_500_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$CPP_ERR_PORT/execute")
    cpp_500_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$CPP_ERR_PORT/execute")
    cpp_500_keys=$(echo "$cpp_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
    if [[ "$go_500_code" == "$cpp_500_code" && "$go_500_keys" == "$cpp_500_keys" ]]; then
      cpp_has_err=$(echo "$cpp_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('intentional' in d.get('error',''))")
      if [[ "$cpp_has_err" == "True" ]]; then
        cpp_srv_pass=$((cpp_srv_pass + 1))
        echo "    [16] POST /execute (runtime error) → 500 + body keys + error contains 'intentional' (Go vs C++)"
      else
        fail "server HTTP: 500 error message mismatch (C++=$cpp_has_err)"
      fi
    else
      fail "server HTTP: 500 response divergence (Go=$go_500_code keys=$go_500_keys, C++=$cpp_500_code keys=$cpp_500_keys)"
    fi
  fi

  srv_cleanup
else
  fail "server HTTP: error-config servers failed to start"
  srv_cleanup
fi

# Third server trio: test warnings format (Redis unreachable + fail_on_error=false)
SRV_WARN_CONFIG="$WORK_DIR/srv_warn_config.json"
cat > "$SRV_WARN_CONFIG" << 'CFGEOF'
{
  "resource_config": {
    "redis_conn": {
      "type": "redis_connection",
      "interval": -1,
      "params": {"addr": "127.0.0.1:1"}
    }
  },
  "pipeline_config": {
    "operators": {
      "redis_getter": {
        "type_name": "transform_redis_get",
        "resource_name": "redis_conn",
        "key_prefix": "test:",
        "fail_on_error": false,
        "$metadata": {
          "common_input": ["uid"],
          "common_output": ["result", "cache_hit"],
          "item_input": [],
          "item_output": []
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["redis_getter"]}
  },
  "flow_contract": {
    "common_input": ["uid"],
    "item_input": [],
    "common_output": ["uid", "result", "cache_hit"],
    "item_output": []
  }
}
CFGEOF

GO_WARN_PORT=18009
JAVA_WARN_PORT=18010
CPP_WARN_PORT=18012

"$WORK_DIR/pineapple-server" -config "$SRV_WARN_CONFIG" -addr ":$GO_WARN_PORT" &
GO_SRV_PID=$!

java -cp "$JAVA_CP" -Dpine.config="$SRV_WARN_CONFIG" -Dpine.port=$JAVA_WARN_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

CPP_SRV_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$SRV_WARN_CONFIG" -addr ":$CPP_WARN_PORT" 2>/dev/null &
  CPP_SRV_PID=$!
fi

cpp_warn_ready=false
if [[ -n "${CPP_SERVER:-}" ]]; then
  if srv_ready $CPP_WARN_PORT; then
    cpp_warn_ready=true
  fi
fi

if srv_ready $GO_WARN_PORT && srv_ready $JAVA_WARN_PORT; then
  # Test 17: POST /execute with warning-producing config → 200 + warnings field parity
  srv_total=$((srv_total + 1))
  WARN_REQ='{"common":{"uid":"x"},"items":[]}'
  go_warn_resp=$(curl -s -w "\n%{http_code}" -X POST -H "Content-Type: application/json" -d "$WARN_REQ" "http://localhost:$GO_WARN_PORT/execute")
  go_warn_code="${go_warn_resp##*$'\n'}"
  go_warn_body="${go_warn_resp%$'\n'*}"
  java_warn_resp=$(curl -s -w "\n%{http_code}" -X POST -H "Content-Type: application/json" -d "$WARN_REQ" "http://localhost:$JAVA_WARN_PORT/execute")
  java_warn_code="${java_warn_resp##*$'\n'}"
  java_warn_body="${java_warn_resp%$'\n'*}"

  if [[ "$go_warn_code" == "200" && "$java_warn_code" == "200" ]]; then
    go_warn_prefix=$(echo "$go_warn_body" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ws = d.get('warnings', [])
if ws:
    w = ws[0]
    idx = w.find('): ')
    print(w[:idx+1] if idx >= 0 else w)
else:
    print('')
")
    java_warn_prefix=$(echo "$java_warn_body" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ws = d.get('warnings', [])
if ws:
    w = ws[0]
    idx = w.find('): ')
    print(w[:idx+1] if idx >= 0 else w)
else:
    print('')
")
    if [[ -n "$go_warn_prefix" && "$go_warn_prefix" == "$java_warn_prefix" ]]; then
      srv_pass=$((srv_pass + 1))
      echo "    [17] POST /execute (warning) → 200 + warnings prefix Go vs Java match: $go_warn_prefix"
    else
      fail "server HTTP: warning prefix divergence (Go='$go_warn_prefix', Java='$java_warn_prefix')"
    fi
  else
    fail "server HTTP: warning test status code (Go=$go_warn_code, Java=$java_warn_code)"
  fi

  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_warn_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_warn_resp=$(curl -s -w "\n%{http_code}" -X POST -H "Content-Type: application/json" -d "$WARN_REQ" "http://localhost:$CPP_WARN_PORT/execute")
    cpp_warn_code="${cpp_warn_resp##*$'\n'}"
    cpp_warn_body="${cpp_warn_resp%$'\n'*}"
    if [[ "$go_warn_code" == "$cpp_warn_code" ]]; then
      cpp_warn_prefix=$(echo "$cpp_warn_body" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ws = d.get('warnings', [])
if ws:
    w = ws[0]
    idx = w.find('): ')
    print(w[:idx+1] if idx >= 0 else w)
else:
    print('')
")
      go_warn_prefix=$(echo "$go_warn_body" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ws = d.get('warnings', [])
if ws:
    w = ws[0]
    idx = w.find('): ')
    print(w[:idx+1] if idx >= 0 else w)
else:
    print('')
")
      if [[ -n "$go_warn_prefix" && "$go_warn_prefix" == "$cpp_warn_prefix" ]]; then
        cpp_srv_pass=$((cpp_srv_pass + 1))
        echo "    [17] POST /execute (warning) → 200 + warnings prefix Go vs C++ match"
      else
        fail "server HTTP: warning prefix divergence (Go='$go_warn_prefix', C++='$cpp_warn_prefix')"
      fi
    else
      fail "server HTTP: warning test status code (Go=$go_warn_code, C++=$cpp_warn_code)"
    fi
  fi

  srv_cleanup
else
  fail "server HTTP: warning-config servers failed to start"
  srv_cleanup
fi

if [[ $srv_total -gt 0 && $srv_pass -eq $srv_total ]]; then
  pass "server HTTP parity Go vs Java ($srv_pass/$srv_total checks)"
elif [[ $srv_total -eq 0 ]]; then
  pass "server HTTP parity Go vs Java (skipped)"
fi

if [[ -n "${CPP_SERVER:-}" ]]; then
  if [[ $cpp_srv_total -gt 0 && $cpp_srv_pass -eq $cpp_srv_total ]]; then
    pass "server HTTP parity Go vs C++ ($cpp_srv_pass/$cpp_srv_total checks)"
  elif [[ $cpp_srv_total -eq 0 ]]; then
    pass "server HTTP parity Go vs C++ (skipped)"
  fi
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
