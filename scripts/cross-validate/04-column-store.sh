#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 4. Column-store execution parity ----------
echo
echo "==> [4/$TOTAL_SECTIONS] Column-store execution parity (storage_mode=column)"
# All fixtures are re-run with storage_mode forced to "column", including those
# that already declare it.  This verifies row→column equivalence uniformly.

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"
col_pass=0
col_total=0
py_col_pass=0
py_col_total=0

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
    # Write expect_error marker if present
    ee = c.get('expect_error', '')
    with open('$WORK_DIR/col_expect_error_${fname}_' + str(i) + '.txt', 'w') as ef:
        ef.write(ee)
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

  case_results_j=""
  case_results_p=""
  for ((i=0; i<cases; i++)); do
    req_file="$WORK_DIR/col_req_${fname}_${i}.json"
    config_file="$WORK_DIR/col_config_${fname}"
    [[ -f "$req_file" && -f "$config_file" ]] || continue
    col_total=$((col_total + 1))
    py_col_total=$((py_col_total + 1))

    res_args=()
    if [[ -f "$WORK_DIR/col_resources_${fname}.json" ]]; then
      res_args=(-static-resources "$WORK_DIR/col_resources_${fname}.json")
    fi

    # Handle expect_error cases: all engines should fail with matching error
    expect_error=""
    if [[ -f "$WORK_DIR/col_expect_error_${fname}_${i}.txt" ]]; then
      expect_error=$(cat "$WORK_DIR/col_expect_error_${fname}_${i}.txt")
    fi
    if [[ -n "$expect_error" ]]; then
      go_err=$("$WORK_DIR/pineapple-run" -config "$config_file" -request "$req_file" "${res_args[@]}" 2>&1 >/dev/null) && {
        fail "column-store expect_error but Go succeeded: $fname case $i"; case_results_j+="✗"; case_results_p+="✗"; continue
      }
      java_err=$(java_run page.liam.pine.RunCli -config "$config_file" -request "$req_file" "${res_args[@]}" 2>&1 >/dev/null) && {
        fail "column-store expect_error but Java succeeded: $fname case $i"; case_results_j+="✗"; case_results_p+="✗"; continue
      }

      py_res_args=()
      if [[ -f "$WORK_DIR/col_resources_${fname}.json" ]]; then
        py_res_args=(-static-resources "$WORK_DIR/col_resources_${fname}.json")
      fi
      py_err=$(py_run pine.cli.run -config "$config_file" -request "$req_file" "${py_res_args[@]}" 2>&1 >/dev/null) && {
        fail "column-store expect_error but Python succeeded: $fname case $i"; case_results_j+="✗"; case_results_p+="✗"; continue
      }

      # Verify all contain the expected substring
      err_ok=true
      if [[ "$go_err" != *"$expect_error"* ]]; then
        fail "column-store Go error mismatch: $fname case $i (want '$expect_error', got '$go_err')"
        err_ok=false
      fi
      if [[ "$java_err" != *"$expect_error"* ]]; then
        fail "column-store Java error mismatch: $fname case $i (want '$expect_error', got '$java_err')"
        err_ok=false
      fi
      if [[ "$py_err" != *"$expect_error"* ]]; then
        fail "column-store Python error mismatch: $fname case $i (want '$expect_error', got '$py_err')"
        err_ok=false
      fi

      if [[ "$err_ok" == "true" ]]; then
        col_pass=$((col_pass + 1))
        py_col_pass=$((py_col_pass + 1))
        case_results_j+="✓"
        case_results_p+="✓"
      else
        case_results_j+="✗"
        case_results_p+="✗"
      fi
      continue
    fi

    go_result=$("$WORK_DIR/pineapple-run" -config "$config_file" -request "$req_file" "${res_args[@]}" 2>/dev/null) || {
      fail "column-store Go failed: $fname case $i"; continue
    }

    java_result=$(java_run page.liam.pine.RunCli -config "$config_file" -request "$req_file" "${res_args[@]}" 2>/dev/null) || {
      fail "column-store Java failed: $fname case $i"; continue
    }

    go_norm=$(echo "$go_result" | normalize_json)
    java_norm=$(echo "$java_result" | normalize_json)

    if [[ "$go_norm" == "$java_norm" ]]; then
      col_pass=$((col_pass + 1))
      case_results_j+="✓"
    else
      fail "column-store divergence (Go vs Java): $fname case $i"
      diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$java_norm" | python3 -m json.tool) >&2 || true
      case_results_j+="✗"
    fi

    # Go vs Python column-store
    py_res_args=()
    if [[ -f "$WORK_DIR/col_resources_${fname}.json" ]]; then
      py_res_args=(-static-resources "$WORK_DIR/col_resources_${fname}.json")
    fi

    py_result=$(py_run pine.cli.run -config "$config_file" -request "$req_file" "${py_res_args[@]}" 2>/dev/null) || {
      fail "column-store Python failed: $fname case $i"; case_results_p+="✗"; continue
    }

    py_norm=$(echo "$py_result" | normalize_json)

    if [[ "$go_norm" == "$py_norm" ]]; then
      py_col_pass=$((py_col_pass + 1))
      case_results_p+="✓"
    else
      fail "column-store divergence (Go vs Python): $fname case $i"
      diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$py_norm" | python3 -m json.tool) >&2 || true
      case_results_p+="✗"
    fi
  done
  echo "    $fname ($cases cases) [J${case_results_j}P${case_results_p}]"
done

if [[ $col_total -gt 0 && $col_pass -eq $col_total ]]; then
  pass "column-store execution parity Go vs Java ($col_pass/$col_total cases)"
elif [[ $col_total -eq 0 ]]; then
  pass "column-store execution parity Go vs Java (no cases, skipped)"
fi

if [[ $py_col_total -gt 0 && $py_col_pass -eq $py_col_total ]]; then
  pass "column-store execution parity Go vs Python ($py_col_pass/$py_col_total cases)"
elif [[ $py_col_total -eq 0 ]]; then
  pass "column-store execution parity Go vs Python (no cases, skipped)"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
