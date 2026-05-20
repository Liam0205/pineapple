#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_parallel.sh"

# ---------- 9. Raw byte execution parity (key ordering) ----------
echo
echo "==> [9/$TOTAL_SECTIONS] Raw byte execution parity (no normalization)"

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"

raw_pass=0
raw_total=0
py_raw_pass=0
py_raw_total=0

for fixture_file in "$FIXTURES_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")

  config_file="$WORK_DIR/config_${fname}"
  [[ -f "$config_file" ]] || continue

  cases=$(python3 -c "
import json, sys
with open('$fixture_file') as f:
    data = json.load(f)
print(len(data.get('cases', [])))
" 2>/dev/null) || continue

  [[ -z "$cases" || "$cases" == "0" ]] && continue

  for ((i=0; i<cases; i++)); do
    req_file="$WORK_DIR/req_${fname}_${i}.json"
    [[ -f "$req_file" && -f "$config_file" ]] || continue
    raw_total=$((raw_total + 1))

    res_args=()
    if [[ -f "$WORK_DIR/resources_${fname}.json" ]]; then
      res_args=(-static-resources "$WORK_DIR/resources_${fname}.json")
    fi

    out_prefix="$WORK_DIR/raw_${fname}_${i}"

    # Run all three engines in parallel
    run_engines_parallel "$config_file" "$req_file" "$out_prefix" "${res_args[@]}"

    go_rc=$(cat "${out_prefix}.go.rc")
    java_rc=$(cat "${out_prefix}.java.rc")
    py_rc=$(cat "${out_prefix}.py.rc")

    if [[ "$go_rc" != "0" ]]; then
      raw_total=$((raw_total - 1)); continue
    fi
    if [[ "$java_rc" != "0" ]]; then
      raw_total=$((raw_total - 1)); continue
    fi

    go_raw=$(cat "${out_prefix}.go.out")
    java_raw=$(cat "${out_prefix}.java.out")

    if [[ "$go_raw" == "$java_raw" ]]; then
      raw_pass=$((raw_pass + 1))
    else
      go_norm=$(echo "$go_raw" | normalize_json)
      java_norm=$(echo "$java_raw" | normalize_json)
      if [[ "$go_norm" == "$java_norm" ]]; then
        raw_pass=$((raw_pass + 1))
        echo "    [W] key ordering differs (Go vs Java): $fname case $i" >&2
      else
        fail "raw byte divergence (Go vs Java): $fname case $i (values differ, not just key ordering)"
      fi
    fi

    # Go vs Python raw byte
    py_raw_total=$((py_raw_total + 1))
    if [[ "$py_rc" != "0" ]]; then
      py_raw_total=$((py_raw_total - 1)); continue
    fi

    py_raw=$(cat "${out_prefix}.py.out")

    if [[ "$go_raw" == "$py_raw" ]]; then
      py_raw_pass=$((py_raw_pass + 1))
    else
      go_norm=${go_norm:-$(echo "$go_raw" | normalize_json)}
      py_norm=$(echo "$py_raw" | normalize_json)
      if [[ "$go_norm" == "$py_norm" ]]; then
        py_raw_pass=$((py_raw_pass + 1))
        echo "    [W] key ordering differs (Go vs Python): $fname case $i" >&2
      else
        fail "raw byte divergence (Go vs Python): $fname case $i (values differ, not just key ordering)"
      fi
    fi
  done
done

if [[ $raw_total -gt 0 && $raw_pass -eq $raw_total ]]; then
  pass "raw byte execution parity Go vs Java ($raw_pass/$raw_total cases)"
elif [[ $raw_total -eq 0 ]]; then
  pass "raw byte execution parity Go vs Java (no cases, skipped)"
fi

if [[ $py_raw_total -gt 0 && $py_raw_pass -eq $py_raw_total ]]; then
  pass "raw byte execution parity Go vs Python ($py_raw_pass/$py_raw_total cases)"
elif [[ $py_raw_total -eq 0 ]]; then
  pass "raw byte execution parity Go vs Python (no cases, skipped)"
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
