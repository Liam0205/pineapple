#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 1. Codegen schema parity ----------
echo "==> [1/$TOTAL_SECTIONS] Codegen schema parity"
echo "    Exporting Go schema..."
"$WORK_DIR/pineapple-codegen" -schema-json "$WORK_DIR/schema-go.json"

echo "    Exporting Java schema..."
java_run page.liam.pine.Codegen --export-schema "$WORK_DIR/schema-java.json"

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
                'templatable': pspec.get('Templatable', False),
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
                'templatable': pspec.get('Templatable', False),
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

# 1c. Codegen Python output byte-level parity
echo "    Comparing generated Python output..."
"$WORK_DIR/pineapple-codegen" -output "$WORK_DIR/python-go" >/dev/null 2>&1
java_run page.liam.pine.Codegen --schema-from-registry -output "$WORK_DIR/python-java" >/dev/null 2>&1
if diff -r "$WORK_DIR/python-go" "$WORK_DIR/python-java" >/dev/null 2>&1; then
  pass "codegen Python output parity (byte-level match)"
else
  fail "codegen Python output divergence"
  diff -r "$WORK_DIR/python-go" "$WORK_DIR/python-java" >&2 || true
fi

# 1d. Codegen Python output Go vs C++ byte-level parity.
# Distinguishes "no cpp codegen binary" (skipped) from "cpp present and
# diverges" (fail) so a regression to byte-level parity doesn't hide silently.
if [[ -n "${CPP_CODEGEN:-}" ]]; then
  rm -rf "$WORK_DIR/python-cpp"
  mkdir -p "$WORK_DIR/python-cpp"
  if ! "$CPP_CODEGEN" -output "$WORK_DIR/python-cpp" >/dev/null 2>&1; then
    fail "C++ codegen failed to produce Python output"
  elif diff -r "$WORK_DIR/python-go" "$WORK_DIR/python-cpp" >/dev/null 2>&1; then
    pass "codegen Python output parity Go vs C++ (byte-level match)"
  else
    fail "codegen Python output divergence (Go vs C++)"
    diff -r "$WORK_DIR/python-go" "$WORK_DIR/python-cpp" >&2 || true
  fi
else
  echo "    (C++ codegen binary not found — skipping C++ Python output parity)"
fi

# 1e. Codegen Markdown output Go vs Java vs C++ byte-level parity.
# Reads each backend's generated doc/operators/ tree and diff -r them
# pairwise. Drift here used to silently sneak through (the 2026-06-22
# review of .code-review/from-24975c2/... surfaced three rendering bugs
# that the Python-only checks above did not catch). pine-cpp learned a
# markdown emit path in the same change; the gate now covers all three
# engines symmetrically.
echo "    Comparing generated Markdown docs (operator reference)..."
rm -rf "$WORK_DIR/docs-go" "$WORK_DIR/docs-java" "$WORK_DIR/docs-cpp"
mkdir -p "$WORK_DIR/docs-go" "$WORK_DIR/docs-java"
"$WORK_DIR/pineapple-codegen" \
  -output "$WORK_DIR/python-go-for-docs" \
  -doc-dir "$WORK_DIR/docs-go" \
  -operators-dir "$REPO_ROOT/pine-go/operators" >/dev/null 2>&1
java_run page.liam.pine.Codegen --schema-from-registry \
  -output "$WORK_DIR/python-java-for-docs" \
  -doc-dir "$WORK_DIR/docs-java" \
  -ops-dir "$REPO_ROOT/pine-java/src/main/java/page/liam/pine/operators" >/dev/null 2>&1
if diff -r "$WORK_DIR/docs-go" "$WORK_DIR/docs-java" >/dev/null 2>&1; then
  pass "codegen Markdown output parity Go vs Java (byte-level match)"
else
  fail "codegen Markdown output divergence (Go vs Java)"
  diff -r "$WORK_DIR/docs-go" "$WORK_DIR/docs-java" >&2 || true
fi

if [[ -n "${CPP_CODEGEN:-}" ]]; then
  mkdir -p "$WORK_DIR/docs-cpp"
  if ! "$CPP_CODEGEN" -doc-dir "$WORK_DIR/docs-cpp" >/dev/null 2>&1; then
    fail "C++ codegen failed to produce Markdown output"
  elif diff -r "$WORK_DIR/docs-go" "$WORK_DIR/docs-cpp" >/dev/null 2>&1; then
    pass "codegen Markdown output parity Go vs C++ (byte-level match)"
  else
    fail "codegen Markdown output divergence (Go vs C++)"
    diff -r "$WORK_DIR/docs-go" "$WORK_DIR/docs-cpp" >&2 || true
  fi
else
  echo "    (C++ codegen binary not found — skipping C++ Markdown output parity)"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
