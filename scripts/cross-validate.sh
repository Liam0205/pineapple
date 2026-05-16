#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK_DIR=$(mktemp -d)
trap "rm -rf $WORK_DIR" EXIT

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

echo "    Done."
echo

# ---------- 1. Codegen schema parity ----------
echo "==> [1/3] Codegen schema parity"
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
echo "==> [2/3] Render-DAG parity"

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
done

if [[ $dag_total -gt 0 && $dag_pass -eq $dag_total ]]; then
  pass "render-dag parity ($dag_pass/$dag_total fixtures)"
elif [[ $dag_total -eq 0 ]]; then
  pass "render-dag parity (no fixtures found, skipped)"
fi

# ---------- 3. Dual-engine execution parity ----------
echo
echo "==> [3/3] Execution parity (Go vs Java on same config+request)"

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"
exec_pass=0
exec_total=0

for fixture_file in "$FIXTURES_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")

  # Skip fixtures requiring static_resources
  if grep -q '"static_resources"' "$fixture_file" 2>/dev/null; then
    echo "    [skip] $fname (requires static_resources)"
    continue
  fi

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

    go_result=$("$WORK_DIR/pineapple-run" -config "$config_file" -request "$req_file" 2>/dev/null) || {
      fail "execution Go failed: $fname case $i"; continue
    }

    java_result=$(java_run page.liam.pine.RunCli -config "$config_file" -request "$req_file" 2>/dev/null) || {
      fail "execution Java failed: $fname case $i"; continue
    }

    # Normalize JSON for comparison (unify int/float: 83 == 83.0)
    go_norm=$(echo "$go_result" | python3 -c "
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
" 2>/dev/null)
    java_norm=$(echo "$java_result" | python3 -c "
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
" 2>/dev/null)

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

# ---------- Summary ----------
echo
echo "╔══════════════════════════════════════╗"
echo "║   Cross-Validation Summary           ║"
echo "╚══════════════════════════════════════╝"
echo -e "$summary"

exit $FAIL
