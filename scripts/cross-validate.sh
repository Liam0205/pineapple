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
echo "==> [1/5] Codegen schema parity"
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
echo "==> [2/5] Render-DAG parity"

dag_pass=0
dag_total=0

for fixture in "$REPO_ROOT"/fixtures/pipelines/*.json; do
  [[ -f "$fixture" ]] || continue
  [[ "$fixture" == *.go ]] && continue
  fname=$(basename "$fixture")

  # Skip fixtures requiring static_resources
  if grep -q '"static_resources"' "$fixture" 2>/dev/null; then
    echo "    [skip] $fname (requires static_resources)"
    continue
  fi

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
echo "==> [3/5] Execution parity (Go vs Java on same config+request)"

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
echo "==> [4/5] Column-store execution parity (storage_mode=column)"

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
echo "==> [5/5] Error parity (Go vs Java on invalid configs)"

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

# ---------- Summary ----------
echo
echo "╔══════════════════════════════════════╗"
echo "║   Cross-Validation Summary           ║"
echo "╚══════════════════════════════════════╝"
echo -e "$summary"

exit $FAIL
