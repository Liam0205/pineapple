#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

FAIL=0
summary=""

fail() {
  FAIL=1
  summary+="FAIL: $1\n"
  echo "  ✗ $1" >&2
}

pass() {
  summary+="PASS: $1\n"
  echo "  ✓ $1"
}

# ---------- Pre-build ----------
echo "==> Pre-building binaries..."

echo "    Building Go CLIs..."
cd "$REPO_ROOT/pine-go"
go build -o "$WORK_DIR/pineapple-codegen" ./cmd/pineapple-codegen/
go build -o "$WORK_DIR/pineapple-dag" ./cmd/pineapple-dag/
go build -o "$WORK_DIR/pineapple-run" ./cmd/pineapple-run/

echo "    Compiling Java + resolving classpath..."
cd "$REPO_ROOT/pine-java"
mvn compile -B -q
JAVA_CP="$REPO_ROOT/pine-java/target/classes:$(mvn dependency:build-classpath -B -q -Dmdep.outputFile=/dev/stdout 2>/dev/null | tail -1)"

java_run() {
  java -cp "$JAVA_CP" "$@"
}

normalize_json() {
  python3 -c "
import json, sys
def normalize(obj):
    if isinstance(obj, dict):
        return {k: normalize(v) for k, v in obj.items()}
    elif isinstance(obj, list):
        return [normalize(v) for v in obj]
    elif isinstance(obj, (int, float)):
        return float(obj)
    return obj
print(json.dumps(normalize(json.load(sys.stdin)), sort_keys=True))
"
}

echo "    Done."
echo

# ---------- 1. Codegen schema parity ----------
echo "==> [1/7] Codegen schema parity"
echo "    Exporting Go schema..."
"$WORK_DIR/pineapple-codegen" -schema-json "$WORK_DIR/schema-go.json"

echo "    Exporting Java schema..."
java_run page.liam.pine.Codegen --export-schema "$WORK_DIR/schema-java.json"

echo "    Comparing structural fields (operator names, param types, required)..."
if python3 -c "
import json, sys

def extract_structure(schemas):
    result = {}
    for op in schemas:
        name = op.get('Name', '')
        params = {}
        for pname, pspec in op.get('Params', {}).items():
            params[pname] = {
                'type': pspec.get('Type', ''),
                'required': pspec.get('Required', False),
            }
        result[name] = params
    return result

go_data = json.load(open('$WORK_DIR/schema-go.json'))
java_data = json.load(open('$WORK_DIR/schema-java.json'))

go_struct = extract_structure(go_data)
java_struct = extract_structure(java_data)

if go_struct == java_struct:
    sys.exit(0)
else:
    all_ops = set(go_struct) | set(java_struct)
    for op in sorted(all_ops):
        if op not in go_struct:
            print(f'  Java-only operator: {op}', file=sys.stderr)
        elif op not in java_struct:
            print(f'  Go-only operator: {op}', file=sys.stderr)
        elif go_struct[op] != java_struct[op]:
            print(f'  Divergence in {op}:', file=sys.stderr)
            print(f'    Go:   {go_struct[op]}', file=sys.stderr)
            print(f'    Java: {java_struct[op]}', file=sys.stderr)
    sys.exit(1)
"; then
  pass "codegen schema parity (operator names + param types/required)"
else
  fail "codegen schema structural divergence"
fi

# ---------- 2. Render-DAG parity ----------
echo
echo "==> [2/7] Render-DAG parity"

dag_pass=0
dag_total=0

for fixture in "$REPO_ROOT"/fixtures/pipelines/*.json; do
  [[ -f "$fixture" ]] || continue
  [[ "$fixture" == *.go ]] && continue
  fname=$(basename "$fixture")

  dag_total=$((dag_total + 1))

  # Extract .config from fixture to a temp file
  config_file="$WORK_DIR/dag_config_${fname}"
  python3 -c "
import json
with open('$fixture') as f:
    data = json.load(f)
cfg = data.get('config', data)
with open('$config_file', 'w') as cf:
    json.dump(cfg, cf)
" || { fail "render-dag extract config: $fname"; continue; }

  go_dot=$("$WORK_DIR/pineapple-dag" -config "$config_file" -format dot 2>/dev/null) || {
    fail "render-dag Go failed: $fname"; continue
  }

  java_dot=$(java_run page.liam.pine.RenderDAGCli -config "$config_file" -format dot -collapse 0 2>/dev/null) || {
    fail "render-dag Java failed: $fname"; continue
  }

  if [[ "$go_dot" == "$java_dot" ]]; then
    dag_pass=$((dag_pass + 1))
    echo "    [$dag_total] $fname → match"
  else
    fail "render-dag divergence: $fname"
    diff <(echo "$go_dot") <(echo "$java_dot") >&2 || true
  fi

  # Test collapsed DAG if fixture has non-empty pipeline_map
  if grep -q '"pipeline_map"' "$fixture" 2>/dev/null; then
    for collapse_level in 1 2; do
      dag_total=$((dag_total + 1))

      go_col=$("$WORK_DIR/pineapple-dag" -config "$config_file" -format dot -collapse "$collapse_level" 2>/dev/null) || {
        fail "render-dag Go collapsed=$collapse_level failed: $fname"; continue
      }

      java_col=$(java_run page.liam.pine.RenderDAGCli -config "$config_file" -format dot -collapse "$collapse_level" 2>/dev/null) || {
        fail "render-dag Java collapsed=$collapse_level failed: $fname"; continue
      }

      if [[ "$go_col" == "$java_col" ]]; then
        dag_pass=$((dag_pass + 1))
        echo "    [$dag_total] $fname (collapse=$collapse_level) → match"
      else
        fail "render-dag divergence: $fname (collapse=$collapse_level)"
        diff <(echo "$go_col") <(echo "$java_col") >&2 || true
      fi
    done
  fi
done

if [[ $dag_total -gt 0 && $dag_pass -eq $dag_total ]]; then
  pass "render-dag parity ($dag_pass/$dag_total fixtures)"
elif [[ $dag_total -eq 0 ]]; then
  pass "render-dag parity (no fixtures found, skipped)"
fi

# ---------- 3. Dual-engine execution parity ----------
echo
echo "==> [3/7] Execution parity (Go vs Java on same config+request)"

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"
exec_pass=0
exec_total=0

for fixture_file in "$FIXTURES_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")

  # Pipeline fixtures have a "cases" array with request/expected pairs
  cases=$(python3 -c "
import json, sys
with open('$fixture_file') as f:
    data = json.load(f)
cases = data.get('cases', [])
if not cases:
    sys.exit(0)
for i, c in enumerate(cases):
    req = c.get('request', {})
    with open('$WORK_DIR/req_${fname}_' + str(i) + '.json', 'w') as rf:
        json.dump(req, rf)
# Write static_resources if present
sr = data.get('static_resources')
if sr is not None:
    with open('$WORK_DIR/resources_${fname}.json', 'w') as sf:
        json.dump(sr, sf)
print(len(cases))
" 2>/dev/null) || continue

  [[ -z "$cases" || "$cases" == "0" ]] && continue

  # Extract config
  python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
with open('$WORK_DIR/config_${fname}', 'w') as cf:
    json.dump(data.get('config', {}), cf)
" 2>/dev/null || continue

  case_results=""
  for ((i=0; i<cases; i++)); do
    req_file="$WORK_DIR/req_${fname}_${i}.json"
    config_file="$WORK_DIR/config_${fname}"
    [[ -f "$req_file" && -f "$config_file" ]] || continue
    exec_total=$((exec_total + 1))

    res_flag=""
    if [[ -f "$WORK_DIR/resources_${fname}.json" ]]; then
      res_flag="-static-resources $WORK_DIR/resources_${fname}.json"
    fi

    go_result=$("$WORK_DIR/pineapple-run" -config "$config_file" -request "$req_file" $res_flag 2>/dev/null) || {
      fail "execution Go failed: $fname case $i"; continue
    }

    java_result=$(java_run page.liam.pine.RunCli -config "$config_file" -request "$req_file" $res_flag 2>/dev/null) || {
      fail "execution Java failed: $fname case $i"; continue
    }

    # Normalize JSON for comparison (unify int/float: 83 == 83.0)
    go_norm=$(echo "$go_result" | normalize_json)
    java_norm=$(echo "$java_result" | normalize_json)

    if [[ "$go_norm" == "$java_norm" ]]; then
      exec_pass=$((exec_pass + 1))
      case_results+="✓"
    else
      fail "execution divergence: $fname case $i"
      diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$java_norm" | python3 -m json.tool) >&2 || true
      case_results+="✗"
    fi
  done
  echo "    $fname ($cases cases) [$case_results]"
done

if [[ $exec_total -gt 0 && $exec_pass -eq $exec_total ]]; then
  pass "execution parity ($exec_pass/$exec_total cases)"
elif [[ $exec_total -eq 0 ]]; then
  pass "execution parity (no pipeline fixture cases found, skipped)"
fi

# ---------- 4. Column-store execution parity ----------
echo
echo "==> [4/7] Column-store execution parity (storage_mode=column)"

col_pass=0
col_total=0

for fixture_file in "$FIXTURES_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")

  cases=$(python3 -c "
import json, sys
with open('$fixture_file') as f:
    data = json.load(f)
cases = data.get('cases', [])
if not cases:
    sys.exit(0)
for i, c in enumerate(cases):
    req = c.get('request', {})
    with open('$WORK_DIR/col_req_${fname}_' + str(i) + '.json', 'w') as rf:
        json.dump(req, rf)
sr = data.get('static_resources')
if sr is not None:
    with open('$WORK_DIR/col_resources_${fname}.json', 'w') as sf:
        json.dump(sr, sf)
print(len(cases))
" 2>/dev/null) || continue

  [[ -z "$cases" || "$cases" == "0" ]] && continue

  # Extract config with storage_mode injected
  python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
cfg = data.get('config', {})
cfg['storage_mode'] = 'column'
with open('$WORK_DIR/col_config_${fname}', 'w') as cf:
    json.dump(cfg, cf)
" 2>/dev/null || continue

  case_results=""
  for ((i=0; i<cases; i++)); do
    req_file="$WORK_DIR/col_req_${fname}_${i}.json"
    config_file="$WORK_DIR/col_config_${fname}"
    [[ -f "$req_file" && -f "$config_file" ]] || continue
    col_total=$((col_total + 1))

    res_flag=""
    if [[ -f "$WORK_DIR/col_resources_${fname}.json" ]]; then
      res_flag="-static-resources $WORK_DIR/col_resources_${fname}.json"
    fi

    go_result=$("$WORK_DIR/pineapple-run" -config "$config_file" -request "$req_file" $res_flag 2>/dev/null) || {
      fail "column-store Go failed: $fname case $i"; continue
    }

    java_result=$(java_run page.liam.pine.RunCli -config "$config_file" -request "$req_file" $res_flag 2>/dev/null) || {
      fail "column-store Java failed: $fname case $i"; continue
    }

    go_norm=$(echo "$go_result" | normalize_json)
    java_norm=$(echo "$java_result" | normalize_json)

    if [[ "$go_norm" == "$java_norm" ]]; then
      col_pass=$((col_pass + 1))
      case_results+="✓"
    else
      fail "column-store divergence: $fname case $i"
      diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$java_norm" | python3 -m json.tool) >&2 || true
      case_results+="✗"
    fi
  done
  echo "    $fname ($cases cases) [$case_results]"
done

if [[ $col_total -gt 0 && $col_pass -eq $col_total ]]; then
  pass "column-store execution parity ($col_pass/$col_total cases)"
elif [[ $col_total -eq 0 ]]; then
  pass "column-store execution parity (no cases, skipped)"
fi

# ---------- 5. Error parity ----------
echo
echo "==> [5/7] Error parity (Go vs Java on invalid configs)"

ERRORS_DIR="$REPO_ROOT/fixtures/errors"
err_pass=0
err_total=0

for fixture_file in "$ERRORS_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")
  err_total=$((err_total + 1))

  # Extract config and expected error type
  python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
cfg = data.get('config', {})
with open('$WORK_DIR/err_config_${fname}', 'w') as cf:
    json.dump(cfg, cf)
req = data.get('request', {'common': {}, 'items': []})
with open('$WORK_DIR/err_req_${fname}', 'w') as rf:
    json.dump(req, rf)
" 2>/dev/null || { fail "error fixture parse: $fname"; continue; }

  config_file="$WORK_DIR/err_config_${fname}"
  req_file="$WORK_DIR/err_req_${fname}"

  # Both engines should fail — capture stderr
  go_err=$("$WORK_DIR/pineapple-run" -config "$config_file" -request "$req_file" 2>&1) && {
    fail "error parity: Go succeeded unexpectedly: $fname"; continue
  }

  java_err=$(java_run page.liam.pine.RunCli -config "$config_file" -request "$req_file" 2>&1) && {
    fail "error parity: Java succeeded unexpectedly: $fname"; continue
  }

  # Extract error classification from fixture
  expected_type=$(python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
print(data.get('expected_error', {}).get('type', ''))
" 2>/dev/null)

  expected_contains=$(python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
print(data.get('expected_error', {}).get('message_contains', ''))
" 2>/dev/null)

  # Verify both errors contain expected substring
  go_ok=true
  java_ok=true

  if [[ -n "$expected_contains" ]]; then
    if ! echo "$go_err" | grep -qi "$expected_contains"; then
      go_ok=false
    fi
    if ! echo "$java_err" | grep -qi "$expected_contains"; then
      java_ok=false
    fi
  fi

  if [[ "$go_ok" == "true" && "$java_ok" == "true" ]]; then
    err_pass=$((err_pass + 1))
    echo "    [$err_total] $fname → both failed correctly"
  else
    fail "error parity: $fname (go_match=$go_ok, java_match=$java_ok)"
    echo "      Go:   $go_err" | head -3 >&2
    echo "      Java: $java_err" | head -3 >&2
  fi
done

if [[ $err_total -gt 0 && $err_pass -eq $err_total ]]; then
  pass "error parity ($err_pass/$err_total fixtures)"
elif [[ $err_total -eq 0 ]]; then
  pass "error parity (no error fixtures found, skipped)"
fi

# ---------- 6. Server HTTP parity ----------
echo
echo "==> [6/7] Server HTTP parity (Go vs Java endpoint behavior)"

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

GO_PORT=18901
JAVA_PORT=18902

# Build server binary
echo "    Building Go server..."
cd "$REPO_ROOT/pine-go"
go build -o "$WORK_DIR/pineapple-server" ./cmd/pineapple-server/

# Start Go server
"$WORK_DIR/pineapple-server" -config "$SRV_CONFIG" -addr ":$GO_PORT" &
GO_SRV_PID=$!

# Start Java server
java -cp "$JAVA_CP" -Dpine.config="$SRV_CONFIG" -Dpine.port=$JAVA_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

# Wait for servers to be ready
srv_ready() {
  local port=$1 max_wait=10 elapsed=0
  while ! curl -s "http://localhost:$port/health" >/dev/null 2>&1; do
    sleep 0.2
    elapsed=$((elapsed + 1))
    if [[ $elapsed -ge $((max_wait * 5)) ]]; then
      return 1
    fi
  done
  return 0
}

srv_cleanup() {
  [[ -n "${GO_SRV_PID:-}" ]] && kill $GO_SRV_PID 2>/dev/null || true
  [[ -n "${JAVA_SRV_PID:-}" ]] && kill $JAVA_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID 2>/dev/null || true
  wait $JAVA_SRV_PID 2>/dev/null || true
}
trap 'srv_cleanup; rm -rf "$WORK_DIR"' EXIT

srv_pass=0
srv_total=0

if ! srv_ready $GO_PORT; then
  fail "server HTTP: Go server failed to start"
  srv_cleanup
elif ! srv_ready $JAVA_PORT; then
  fail "server HTTP: Java server failed to start"
  srv_cleanup
else
  echo "    Both servers ready."

  # Test 1: GET /health
  srv_total=$((srv_total + 1))
  go_health=$(curl -s "http://localhost:$GO_PORT/health")
  java_health=$(curl -s "http://localhost:$JAVA_PORT/health")
  if [[ "$go_health" == "$java_health" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [1] GET /health → match"
  else
    fail "server HTTP: /health divergence"
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
    echo "    [2] POST /execute (valid) → match"
  else
    fail "server HTTP: /execute valid request divergence"
    diff <(echo "$go_exec" | python3 -m json.tool) <(echo "$java_exec" | python3 -m json.tool) >&2 || true
  fi

  # Test 3: GET /execute (wrong method) → 405
  srv_total=$((srv_total + 1))
  go_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_PORT/execute")
  java_405_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_PORT/execute")
  go_405_body=$(curl -s "http://localhost:$GO_PORT/execute")
  java_405_body=$(curl -s "http://localhost:$JAVA_PORT/execute")
  if [[ "$go_405_code" == "$java_405_code" ]] && [[ "$go_405_code" == "405" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [3] GET /execute → 405 match"
  else
    fail "server HTTP: /execute wrong method (Go=$go_405_code, Java=$java_405_code)"
  fi

  # Test 4: POST /execute with invalid JSON → 400
  srv_total=$((srv_total + 1))
  go_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$GO_PORT/execute")
  java_400_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$JAVA_PORT/execute")
  if [[ "$go_400_code" == "$java_400_code" ]] && [[ "$go_400_code" == "400" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [4] POST /execute (bad JSON) → 400 match"
  else
    fail "server HTTP: /execute bad JSON (Go=$go_400_code, Java=$java_400_code)"
  fi

  # Test 5: GET /dag → DOT output parity
  srv_total=$((srv_total + 1))
  go_dag=$(curl -s "http://localhost:$GO_PORT/dag")
  java_dag=$(curl -s "http://localhost:$JAVA_PORT/dag")
  if [[ "$go_dag" == "$java_dag" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [5] GET /dag → match"
  else
    fail "server HTTP: /dag divergence"
    diff <(echo "$go_dag") <(echo "$java_dag") >&2 || true
  fi

  # Test 6: GET /stats → structure parity (compare after execute)
  srv_total=$((srv_total + 1))
  go_stats_keys=$(curl -s "http://localhost:$GO_PORT/stats" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  java_stats_keys=$(curl -s "http://localhost:$JAVA_PORT/stats" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  if [[ "$go_stats_keys" == "$java_stats_keys" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [6] GET /stats → top-level keys match"
  else
    fail "server HTTP: /stats keys divergence (Go=$go_stats_keys, Java=$java_stats_keys)"
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
    echo "    [7] GET /stats → operator stat keys match"
  else
    fail "server HTTP: /stats operator keys divergence (Go=$go_op_keys, Java=$java_op_keys)"
  fi

  # Test 8: POST /execute (bad JSON) → verify 400 body contains "error" field
  srv_total=$((srv_total + 1))
  go_400_body=$(curl -s -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$GO_PORT/execute")
  java_400_body=$(curl -s -X POST -H "Content-Type: application/json" -d "not json" "http://localhost:$JAVA_PORT/execute")
  go_400_has_error=$(echo "$go_400_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('error' in d)")
  java_400_has_error=$(echo "$java_400_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('error' in d)")
  if [[ "$go_400_has_error" == "True" && "$java_400_has_error" == "True" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [8] POST /execute (bad JSON) → 400 body has error field"
  else
    fail "server HTTP: /execute 400 body structure (Go=$go_400_has_error, Java=$java_400_has_error)"
  fi

  # Test 9: POST /execute (missing required field) → 400 ValidationError
  srv_total=$((srv_total + 1))
  go_val_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$GO_PORT/execute")
  java_val_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{}]}' "http://localhost:$JAVA_PORT/execute")
  if [[ "$go_val_code" == "$java_val_code" ]]; then
    srv_pass=$((srv_pass + 1))
    echo "    [9] POST /execute (missing field) → $go_val_code match"
  else
    fail "server HTTP: ValidationError status divergence (Go=$go_val_code, Java=$java_val_code)"
  fi

  srv_cleanup
fi

# Second server pair: test 500 partial result body (Lua error config)
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

GO_ERR_PORT=18903
JAVA_ERR_PORT=18904

"$WORK_DIR/pineapple-server" -config "$SRV_ERR_CONFIG" -addr ":$GO_ERR_PORT" &
GO_SRV_PID=$!

java -cp "$JAVA_CP" -Dpine.config="$SRV_ERR_CONFIG" -Dpine.port=$JAVA_ERR_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

if srv_ready $GO_ERR_PORT && srv_ready $JAVA_ERR_PORT; then
  # Test 10: POST /execute (runtime error) → 500 + error field + body structure
  srv_total=$((srv_total + 1))
  go_500_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$GO_ERR_PORT/execute")
  java_500_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$JAVA_ERR_PORT/execute")
  go_500_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$GO_ERR_PORT/execute")
  java_500_body=$(curl -s -X POST -H "Content-Type: application/json" -d '{"common":{},"items":[{"x":1}]}' "http://localhost:$JAVA_ERR_PORT/execute")

  go_500_keys=$(echo "$go_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")
  java_500_keys=$(echo "$java_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print(sorted(d.keys()))")

  if [[ "$go_500_code" == "500" && "$java_500_code" == "500" && "$go_500_keys" == "$java_500_keys" ]]; then
    # Both 500 with same key structure
    go_has_err=$(echo "$go_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('intentional' in d.get('error',''))")
    java_has_err=$(echo "$java_500_body" | python3 -c "import json,sys; d=json.load(sys.stdin); print('intentional' in d.get('error',''))")
    if [[ "$go_has_err" == "True" && "$java_has_err" == "True" ]]; then
      srv_pass=$((srv_pass + 1))
      echo "    [10] POST /execute (runtime error) → 500 + body keys match + error contains 'intentional'"
    else
      fail "server HTTP: 500 error message mismatch (Go=$go_has_err, Java=$java_has_err)"
    fi
  else
    fail "server HTTP: 500 response divergence (Go=$go_500_code keys=$go_500_keys, Java=$java_500_code keys=$java_500_keys)"
  fi

  srv_cleanup
else
  fail "server HTTP: error-config servers failed to start"
  srv_cleanup
fi

if [[ $srv_total -gt 0 && $srv_pass -eq $srv_total ]]; then
  pass "server HTTP parity ($srv_pass/$srv_total checks)"
elif [[ $srv_total -eq 0 ]]; then
  pass "server HTTP parity (skipped)"
fi

# ---------- 7. Cancellation/timeout parity ----------
echo
echo "==> [7/7] Cancellation parity (timeout behavior)"

# Create a slow Lua fixture that should exceed a tight timeout
TIMEOUT_CONFIG="$WORK_DIR/timeout_config.json"
TIMEOUT_REQ="$WORK_DIR/timeout_req.json"

cat > "$TIMEOUT_CONFIG" << 'CFGEOF'
{
  "pipeline_config": {
    "operators": {
      "slow_lua": {
        "type_name": "transform_by_lua",
        "lua_script": "function slow()\n  local s=0\n  for i=1,100000000 do s=s+i end\n  return s\nend",
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
timeout 1 "$WORK_DIR/pineapple-run" -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || go_exit=$?
java_exit=0
timeout 1 java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || java_exit=$?

# Both should have been killed (exit 124 from timeout, or 137 from SIGKILL)
if [[ $go_exit -ne 0 && $java_exit -ne 0 ]]; then
  cancel_pass=$((cancel_pass + 1))
  echo "    [1] slow Lua + timeout 1s → both killed (Go=$go_exit, Java=$java_exit)"
elif [[ $go_exit -eq 0 && $java_exit -eq 0 ]]; then
  # Both finished fast enough — still parity
  cancel_pass=$((cancel_pass + 1))
  echo "    [1] slow Lua + timeout 1s → both completed (parity OK)"
else
  fail "cancellation parity: divergence (Go exit=$go_exit, Java exit=$java_exit)"
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
  if echo "$go_lua_err" | grep -qi "intentional" && echo "$java_lua_err" | grep -qi "intentional"; then
    cancel_pass=$((cancel_pass + 1))
    echo "    [2] Lua error() → both failed with expected message"
  else
    fail "cancellation parity: Lua error message mismatch"
    echo "      Go:   $go_lua_err" | head -2 >&2
    echo "      Java: $java_lua_err" | head -2 >&2
  fi
elif [[ "$go_lua_ok" == "$java_lua_ok" ]]; then
  cancel_pass=$((cancel_pass + 1))
  echo "    [2] Lua error() → both behaved same (ok=$go_lua_ok)"
else
  fail "cancellation parity: Lua error divergence (Go_ok=$go_lua_ok, Java_ok=$java_lua_ok)"
fi

if [[ $cancel_total -gt 0 && $cancel_pass -eq $cancel_total ]]; then
  pass "cancellation parity ($cancel_pass/$cancel_total checks)"
elif [[ $cancel_total -eq 0 ]]; then
  pass "cancellation parity (skipped)"
fi

# ---------- Summary ----------
echo
echo "╔══════════════════════════════════════╗"
echo "║   Cross-Validation Summary           ║"
echo "╚══════════════════════════════════════╝"
echo -e "$summary"

exit $FAIL
