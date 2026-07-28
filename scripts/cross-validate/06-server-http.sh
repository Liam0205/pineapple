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
# Turn on debug for every operator so the _return_trace check below actually
# receives input_snapshot / output_snapshot. Without it the trace entries carry
# only name and duration_ms, so the snapshot key ordering that issue #183 is
# about was never present in what this section compared.
for op in cfg.get('pipeline_config', {}).get('operators', {}).values():
    if isinstance(op, dict):
        op['debug'] = True
        # NOTE: the probe keys added to the request below deliberately are NOT
        # declared as common_input here. snapshotInput only emits an operator's
        # declared common_input, so they never reach input_snapshot — meaning
        # they do NOT give this check teeth on input_snapshot wrapping. Adding
        # them to common_input was tried and breaks transform_copy, whose
        # metadata arity must match its output list. What actually pins the
        # snapshot wrapping is the 12-item padding below, via
        # output_snapshot.item_writes. Recorded so the next reader does not
        # assume the probe keys are load-bearing.

# Rename operators so declaration order is NOT alphabetical. /stats.operators is
# a map in Go and therefore sorts by operator name, but this fixture's names
# (copy_score, truncate) are already in alphabetical order — so an engine
# emitting pipeline order looked identical and the /stats checks below passed
# against a real divergence. Prefixing in reverse declaration order guarantees
# the two orders differ.
pc = cfg.get('pipeline_config', {})
ops = pc.get('operators', {})
if ops:
    names = list(ops)
    # The prefix carries HTML-special characters on purpose. Operator names
    # reach the response through trace[].name and /stats.operators keys, both
    # of which go through the hand-written JSON path — a separate escaping
    # implementation from the Variant writer, and one that lacked Go's
    # HTML-safe escapes until issue #183. An all-ASCII-safe prefix could not
    # detect that. chr(122-i) keeps declaration order reverse-alphabetical for
    # the /stats.operators sort check.
    rename = {n: chr(122 - i) + 'z<&>_' + n for i, n in enumerate(names)}
    pc['operators'] = {rename[n]: ops[n] for n in names}
    pm = pc.get('pipeline_map', {})
    for stage in pm.values():
        if isinstance(stage, dict) and isinstance(stage.get('pipeline'), list):
            stage['pipeline'] = [rename.get(x, x) for x in stage['pipeline']]
    for grp in cfg.get('pipeline_group', {}).values():
        if isinstance(grp, dict) and isinstance(grp.get('pipeline'), list):
            grp['pipeline'] = [rename.get(x, x) for x in grp['pipeline']]
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
# Add extra common keys in NON-sorted declaration order, and pad items past 10.
# A single-key snapshot has no order to get wrong, so without this the check
# passed even against an engine that emitted snapshots in insertion order. The
# item padding matters because output_snapshot.item_writes has integer keys that
# Go renders and sorts as strings, so index 10 must land between 1 and 2.
for k, v in (('zz_probe', 1), ('aa_probe', 2), ('mm_probe', 3)):
    req['common'][k] = v   # inert for input_snapshot - see the NOTE above; kept only as request-shape noise
base_items = req.get('items') or []
if base_items:
    while len(req['items']) < 12:
        clone = dict(base_items[len(req['items']) % len(base_items)])
        req['items'].append(clone)
print(json.dumps(req))
")
  go_trace_body=$(curl -s -X POST -H "Content-Type: application/json" -d "$TRACE_REQ" "http://localhost:$GO_PORT/execute")
  java_trace_body=$(curl -s -X POST -H "Content-Type: application/json" -d "$TRACE_REQ" "http://localhost:$JAVA_PORT/execute")
  go_trace_struct=$(echo "$go_trace_body" | python3 -c "
import collections, json, sys
# object_pairs_hook keeps key ORDER, which sorted(keys) used to discard. The
# trace entry is a struct in Go (declaration order) while input_snapshot and
# output_snapshot inside it are maps (sorted), and output_snapshot.item_writes
# has int keys Go renders and sorts as strings. None of that was comparable
# while this printed sorted(trace[0].keys()) - see issue #183.
d = json.loads(sys.stdin.read(), object_pairs_hook=collections.OrderedDict)
trace = d.get('trace', [])
if trace:
    def shape(node):
        if isinstance(node, dict):
            return [[k, shape(v)] for k, v in node.items()]
        if isinstance(node, list):
            return [shape(v) for v in node]
        return None
    # duration_ms is timing, so compare the key sequence and nested shape only.
    print(f'count={len(trace)} order={json.dumps(shape(trace[0]))}')
else:
    print('no_trace')
")
  java_trace_struct=$(echo "$java_trace_body" | python3 -c "
import collections, json, sys
# object_pairs_hook keeps key ORDER, which sorted(keys) used to discard. The
# trace entry is a struct in Go (declaration order) while input_snapshot and
# output_snapshot inside it are maps (sorted), and output_snapshot.item_writes
# has int keys Go renders and sorts as strings. None of that was comparable
# while this printed sorted(trace[0].keys()) - see issue #183.
d = json.loads(sys.stdin.read(), object_pairs_hook=collections.OrderedDict)
trace = d.get('trace', [])
if trace:
    def shape(node):
        if isinstance(node, dict):
            return [[k, shape(v)] for k, v in node.items()]
        if isinstance(node, list):
            return [shape(v) for v in node]
        return None
    # duration_ms is timing, so compare the key sequence and nested shape only.
    print(f'count={len(trace)} order={json.dumps(shape(trace[0]))}')
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
import collections, json, sys
# object_pairs_hook keeps key ORDER, which sorted(keys) used to discard. The
# trace entry is a struct in Go (declaration order) while input_snapshot and
# output_snapshot inside it are maps (sorted), and output_snapshot.item_writes
# has int keys Go renders and sorts as strings. None of that was comparable
# while this printed sorted(trace[0].keys()) - see issue #183.
d = json.loads(sys.stdin.read(), object_pairs_hook=collections.OrderedDict)
trace = d.get('trace', [])
if trace:
    def shape(node):
        if isinstance(node, dict):
            return [[k, shape(v)] for k, v in node.items()]
        if isinstance(node, list):
            return [shape(v) for v in node]
        return None
    # duration_ms is timing, so compare the key sequence and nested shape only.
    print(f'count={len(trace)} order={json.dumps(shape(trace[0]))}')
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

  # Test 14b: /stats top-level key ORDER parity. Go builds this response as a
  # map[string]any (server.go:760), not a struct, so encoding/json sorts its keys
  # — unlike /execute, whose envelope is a struct and keeps declaration order.
  # Values are runtime counters and timings so only the key sequence is compared.
  srv_total=$((srv_total + 1))
  stats_order_cmd="import collections, json, sys
d = json.loads(sys.stdin.read(), object_pairs_hook=collections.OrderedDict)
def shape(node):
    if isinstance(node, dict):
        return [[k, shape(v)] for k, v in node.items()]
    return None
print(json.dumps(shape(d)))"
  go_stats_order=$(curl -s "http://localhost:$GO_PORT/stats" | python3 -c "$stats_order_cmd")
  java_stats_order=$(curl -s "http://localhost:$JAVA_PORT/stats" | python3 -c "$stats_order_cmd")
  if [[ "$go_stats_order" == "$java_stats_order" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [14b] GET /stats → key order Go vs Java match"
  else
    fail "server HTTP: /stats key order divergence (Go=$go_stats_order, Java=$java_stats_order)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_stats_order=$(curl -s "http://localhost:$CPP_PORT/stats" | python3 -c "$stats_order_cmd")
    if [[ "$go_stats_order" == "$cpp_stats_order" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [14b] GET /stats → key order Go vs C++ match"
    else
      fail "server HTTP: /stats key order divergence (Go=$go_stats_order, C++=$cpp_stats_order)"
    fi
  fi

  # Test 14c: trace input_snapshot key ORDER, on a second fixture whose
  # common_input is declared non-alphabetically (control_op_nil_field_no_crash's
  # ctrl_if declares ["event", "expose_duration"]). The main fixture above cannot
  # pin this: snapshotInput only emits an operator's DECLARED common_input, and
  # transform_copy's arity forbids adding keys to it, so an engine emitting
  # snapshots in insertion order looked correct there.
  srv_total=$((srv_total + 1))
  SNAP_FIXTURE="$REPO_ROOT/fixtures/pipelines/control_op_nil_field_no_crash.json"
  SNAP_CONFIG="$WORK_DIR/snap_config.json"
  python3 -c "
import json
with open('$SNAP_FIXTURE') as f:
    data = json.load(f)
cfg = data['config']
for op in cfg.get('pipeline_config', {}).get('operators', {}).values():
    if isinstance(op, dict):
        op['debug'] = True
        # Reverse each declared common_input so declaration order is NOT sorted
        # order. ctrl_if declares ["event", "expose_duration"], which is already
        # alphabetical — so an engine emitting snapshots in insertion order
        # produced identical bytes and this check passed against a real
        # divergence. Reversing guarantees the two orders differ. The operator
        # reads its inputs by name, so order here does not change behaviour.
        meta = op.get(chr(36) + 'metadata') or {}
        ci = meta.get('common_input')
        if isinstance(ci, list) and len(ci) > 1:
            meta['common_input'] = list(reversed(ci))
with open('$SNAP_CONFIG', 'w') as cf:
    json.dump(cfg, cf)
"
  SNAP_REQ=$(python3 -c "
import json
with open('$SNAP_FIXTURE') as f:
    data = json.load(f)
req = data['cases'][0]['request']
req['common']['_return_trace'] = True
print(json.dumps(req))
")
  snap_order_cmd="import collections, json, sys
d = json.loads(sys.stdin.read(), object_pairs_hook=collections.OrderedDict)
def shape(node):
    if isinstance(node, dict):
        return [[k, shape(v)] for k, v in node.items()]
    if isinstance(node, list):
        return [shape(v) for v in node]
    return None
snaps = [t.get('input_snapshot') for t in d.get('trace', []) if t.get('input_snapshot')]
print(json.dumps([shape(x) for x in snaps]))"
  SNAP_GO_PORT=18021
  SNAP_JAVA_PORT=18022
  "$WORK_DIR/pineapple-server" -config "$SNAP_CONFIG" -addr ":$SNAP_GO_PORT" >/dev/null 2>&1 &
  snap_go_pid=$!
  java -cp "$JAVA_CP" -Dpine.config="$SNAP_CONFIG" -Dpine.port=$SNAP_JAVA_PORT page.liam.pine.PineServer >/dev/null 2>&1 &
  snap_java_pid=$!
  # srv_ready, not a fixed sleep. Under set -euo pipefail an unready server makes
  # curl return empty, python3 exit 1 on JSONDecodeError, and pipefail abort the
  # whole section at this line — silently truncating every later check instead of
  # printing one red. The `|| echo parse_error` keeps a slow start visible as a
  # failure rather than a truncation. Note 14c compares Go and Java only: C++'s
  # snapshot_input omits null values, a pre-existing value difference unrelated to
  # key order, so including it here would fail for the wrong reason.
  srv_ready $SNAP_GO_PORT || fail "server HTTP: 14c Go server not ready"
  srv_ready $SNAP_JAVA_PORT || fail "server HTTP: 14c Java server not ready"
  snap_go=$(curl -s -X POST -H "Content-Type: application/json" -d "$SNAP_REQ" "http://localhost:$SNAP_GO_PORT/execute" | python3 -c "$snap_order_cmd" || echo parse_error)
  snap_java=$(curl -s -X POST -H "Content-Type: application/json" -d "$SNAP_REQ" "http://localhost:$SNAP_JAVA_PORT/execute" | python3 -c "$snap_order_cmd" || echo parse_error)
  kill $snap_go_pid $snap_java_pid 2>/dev/null || true
  wait $snap_go_pid $snap_java_pid 2>/dev/null || true
  if [[ "$snap_go" == "$snap_java" && "$snap_go" != "[]" && "$snap_go" != "parse_error" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [14c] POST /execute (trace input_snapshot) → key order Go vs Java match"
  else
    fail "server HTTP: trace input_snapshot key order divergence (Go=$snap_go, Java=$snap_java)"
  fi

  # Test 14d: trace duration_ms NUMBER FORMAT. The value is timing and therefore
  # not comparable, but its spelling is: Go marshals float64 with
  # shortest-round-trip, so it never uses exponent notation in this range and
  # never truncates to 6 significant digits. snprintf("%g") does both — 1234.567
  # became 1234.57 and 1234567 became 1.23457e+06 — and no channel could see it,
  # because every one of them strips duration_ms as timing. This checks the shape
  # instead of the number, which is deterministic.
  srv_total=$((srv_total + 1))
  dur_shape_cmd="import json, re, sys
d = json.loads(sys.stdin.read())
bad = []
for t in d.get('trace', []):
    raw = t.get('duration_ms')
    txt = repr(raw)
    if 'e' in txt.lower():
        bad.append('exponent notation: ' + txt)
    digits = re.sub(r'[^0-9]', '', txt).lstrip('0')
    if len(digits) == 6 and float(raw) >= 1000:
        bad.append('suspiciously exactly 6 significant digits: ' + txt)
print('ok' if not bad else '; '.join(bad))"
  # Uses its own config with transform_bench_cpu so the duration is non-trivial.
  #
  # Honest limit of this check: duration_ms has microsecond resolution (at most
  # three decimals), so "%g" and shortest-round-trip only disagree once the value
  # exceeds 1000 ms. A 400k-iteration bench operator measures about 4 ms, so this
  # check confirms the shape is exponent-free and untruncated but does NOT go red
  # against snprintf("%g") — verified by mutation. Making it discriminate would
  # need a deliberately >1s operator, which is too slow for this suite. The
  # formatter itself is pinned by test_json.cpp's
  # "trace duration magnitudes match Go" case instead.
  DUR_CONFIG="$WORK_DIR/dur_config.json"
  python3 -c "
import json
cfg = {'pipeline_config': {'operators': {'slow': {'type_name': 'transform_bench_cpu',
        'iterations': 400000, 'debug': True,
        chr(36)+'metadata': {'item_input': [], 'item_output': []}}},
       'pipeline_map': {'s': {'pipeline': ['slow']}}},
       'pipeline_group': {'main': {'pipeline': ['s']}}}
with open('$DUR_CONFIG', 'w') as f:
    json.dump(cfg, f)
"
  DUR_GO_PORT=18031
  DUR_JAVA_PORT=18032
  DUR_CPP_PORT=18033
  "$WORK_DIR/pineapple-server" -config "$DUR_CONFIG" -addr ":$DUR_GO_PORT" >/dev/null 2>&1 &
  dur_go_pid=$!
  java -cp "$JAVA_CP" -Dpine.config="$DUR_CONFIG" -Dpine.port=$DUR_JAVA_PORT page.liam.pine.PineServer >/dev/null 2>&1 &
  dur_java_pid=$!
  dur_cpp_pid=""
  if [[ -n "${CPP_SERVER:-}" ]]; then
    "$CPP_SERVER" -config "$DUR_CONFIG" -addr ":$DUR_CPP_PORT" >/dev/null 2>&1 &
    dur_cpp_pid=$!
  fi
  srv_ready $DUR_GO_PORT || fail "server HTTP: 14d Go server not ready"
  srv_ready $DUR_JAVA_PORT || fail "server HTTP: 14d Java server not ready"
  DUR_REQ='{"common":{"_return_trace":true},"items":[{"id":"a"}]}'
  go_dur=$(curl -s -X POST -H "Content-Type: application/json" -d "$DUR_REQ" "http://localhost:$DUR_GO_PORT/execute" | python3 -c "$dur_shape_cmd" || echo parse_error)
  java_dur=$(curl -s -X POST -H "Content-Type: application/json" -d "$DUR_REQ" "http://localhost:$DUR_JAVA_PORT/execute" | python3 -c "$dur_shape_cmd" || echo parse_error)
  if [[ "$go_dur" == "ok" && "$java_dur" == "ok" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [14d] POST /execute (trace) → duration_ms number format Go and Java both shortest-round-trip"
  else
    fail "server HTTP: duration_ms number format (Go=$go_dur, Java=$java_dur)"
  fi
  if [[ -n "${CPP_SERVER:-}" ]] && $cpp_srv_ready; then
    cpp_srv_total=$((cpp_srv_total + 1))
    cpp_dur=$(curl -s -X POST -H "Content-Type: application/json" -d "$DUR_REQ" "http://localhost:$DUR_CPP_PORT/execute" | python3 -c "$dur_shape_cmd" || echo parse_error)
    if [[ "$cpp_dur" == "ok" ]]; then
      cpp_srv_pass=$((cpp_srv_pass + 1))
      echo "    [14d] POST /execute (trace) → duration_ms number format Go vs C++ match"
    else
      fail "server HTTP: duration_ms number format C++ (=$cpp_dur)"
    fi
  fi
  kill $dur_go_pid $dur_java_pid ${dur_cpp_pid:-} 2>/dev/null || true
  wait $dur_go_pid $dur_java_pid ${dur_cpp_pid:-} 2>/dev/null || true

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
