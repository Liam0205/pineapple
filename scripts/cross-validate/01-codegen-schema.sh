#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 1. Codegen schema parity ----------
echo "==> [1/$TOTAL_SECTIONS] Codegen schema parity"
echo "    Exporting Go schema..."
"$WORK_DIR/pineapple-codegen" -schema-json "$WORK_DIR/schema-go.json"

echo "    Exporting Java schema..."
java_run page.liam.pine.Codegen --export-schema "$WORK_DIR/schema-java.json"

echo "    Exporting Python schema..."
py_run pine.cli.codegen --export-schema "$WORK_DIR/schema-python.json"

echo "    Comparing structural fields (operator names, param types, required, defaults)..."
if python3 -c "
import json, sys

def normalize_value(v):
    if isinstance(v, (int, float)):
        return float(v)
    return v

def extract_structure(schemas):
    result = {}
    for op in schemas:
        name = op.get('Name', '')
        params = {}
        for pname, pspec in op.get('Params', {}).items():
            params[pname] = {
                'type': pspec.get('Type', ''),
                'required': pspec.get('Required', False),
                'default': normalize_value(pspec.get('Default')),
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
            for p in sorted(set(go_struct[op]) | set(java_struct[op])):
                gv = go_struct[op].get(p)
                jv = java_struct[op].get(p)
                if gv != jv:
                    print(f'    {p}: Go={gv} Java={jv}', file=sys.stderr)
    sys.exit(1)
"; then
  pass "codegen schema parity Go vs Java (operator names + param types/required/defaults)"
else
  fail "codegen schema structural divergence (Go vs Java)"
fi

# 1b. Go vs C++ schema parity
if [[ -n "${CPP_CODEGEN:-}" ]]; then
  echo "    Exporting C++ schema..."
  "$CPP_CODEGEN" -schema-json "$WORK_DIR/schema-cpp.json"
  echo "    Comparing Go vs C++ schema structure..."
  if python3 -c "
import json, sys

def normalize_value(v):
    if isinstance(v, (int, float)):
        return float(v)
    return v

def extract_structure(schemas):
    result = {}
    for op in schemas:
        name = op.get('Name', '')
        params = {}
        for pname, pspec in op.get('Params', {}).items():
            params[pname] = {
                'type': pspec.get('Type', ''),
                'required': pspec.get('Required', False),
                'default': normalize_value(pspec.get('Default')),
            }
        result[name] = params
    return result

go_data = json.load(open('$WORK_DIR/schema-go.json'))
cpp_data = json.load(open('$WORK_DIR/schema-cpp.json'))

go_struct = extract_structure(go_data)
cpp_struct = extract_structure(cpp_data)

if go_struct == cpp_struct:
    sys.exit(0)
else:
    all_ops = set(go_struct) | set(cpp_struct)
    for op in sorted(all_ops):
        if op not in go_struct:
            print(f'  C++-only operator: {op}', file=sys.stderr)
        elif op not in cpp_struct:
            print(f'  Go-only operator: {op}', file=sys.stderr)
        elif go_struct[op] != cpp_struct[op]:
            print(f'  Divergence in {op}:', file=sys.stderr)
            for p in sorted(set(go_struct[op]) | set(cpp_struct[op])):
                gv = go_struct[op].get(p)
                cv = cpp_struct[op].get(p)
                if gv != cv:
                    print(f'    {p}: Go={gv} C++={cv}', file=sys.stderr)
    sys.exit(1)
"; then
    pass "codegen schema parity Go vs C++ (operator names + param types/required/defaults)"
  else
    fail "codegen schema structural divergence (Go vs C++)"
  fi
else
  echo "    (C++ codegen binary not found — skipping C++ schema parity)"
fi

# 1c. Go vs Python schema parity
echo "    Comparing Go vs Python schema structure..."
if python3 -c "
import json, sys

# Operators intentionally absent from pine-python (Go-only).
# Add here when a new Go-only operator is registered.
GO_ONLY_OPERATORS = {
    'transform_bench_cpu',
    'transform_bench_sleep',
}

def normalize_value(v):
    if isinstance(v, (int, float)):
        return float(v)
    return v

def extract_structure(schemas, skip=set()):
    result = {}
    for op in schemas:
        name = op.get('Name', '')
        if name in skip:
            continue
        params = {}
        for pname, pspec in op.get('Params', {}).items():
            params[pname] = {
                'type': pspec.get('Type', ''),
                'required': pspec.get('Required', False),
                'default': normalize_value(pspec.get('Default')),
            }
        result[name] = params
    return result

go_data = json.load(open('$WORK_DIR/schema-go.json'))
py_data = json.load(open('$WORK_DIR/schema-python.json'))

go_struct = extract_structure(go_data, skip=GO_ONLY_OPERATORS)
py_struct = extract_structure(py_data)

if go_struct == py_struct:
    sys.exit(0)
else:
    all_ops = set(go_struct) | set(py_struct)
    for op in sorted(all_ops):
        if op not in go_struct:
            print(f'  Python-only operator: {op}', file=sys.stderr)
        elif op not in py_struct:
            print(f'  Go-only operator (unexpected — add to GO_ONLY_OPERATORS if intentional): {op}', file=sys.stderr)
        elif go_struct[op] != py_struct[op]:
            print(f'  Divergence in {op}:', file=sys.stderr)
            for p in sorted(set(go_struct[op]) | set(py_struct[op])):
                gv = go_struct[op].get(p)
                pv = py_struct[op].get(p)
                if gv != pv:
                    print(f'    {p}: Go={gv} Python={pv}', file=sys.stderr)
    sys.exit(1)
"; then
  pass "codegen schema parity Go vs Python (operator names + param types/required/defaults)"
else
  fail "codegen schema structural divergence (Go vs Python)"
fi

# 1b. Codegen Python output byte-level parity
echo "    Comparing generated Python output..."
"$WORK_DIR/pineapple-codegen" -output "$WORK_DIR/python-go" >/dev/null 2>&1
java_run page.liam.pine.Codegen --schema-from-registry -output "$WORK_DIR/python-java" >/dev/null 2>&1
if diff -r "$WORK_DIR/python-go" "$WORK_DIR/python-java" >/dev/null 2>&1; then
  pass "codegen Python output parity (byte-level match)"
else
  fail "codegen Python output divergence"
  diff -r "$WORK_DIR/python-go" "$WORK_DIR/python-java" >&2 || true
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
