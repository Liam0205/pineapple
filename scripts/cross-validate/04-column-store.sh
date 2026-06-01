#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_parallel.sh"

# ---------- 4. Column-store execution parity ----------
echo
echo "==> [4/$TOTAL_SECTIONS] Column-store execution parity (storage_mode=column)"

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"
col_pass=0
col_total=0
cpp_col_pass=0
cpp_col_total=0

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
    ee = c.get('expect_error', '')
    with open('$WORK_DIR/col_expect_error_${fname}_' + str(i) + '.txt', 'w') as ef:
        ef.write(ee)
so = data.get('strict_order', True)
with open('$WORK_DIR/col_strict_order_${fname}.txt', 'w') as sf:
    sf.write(str(so).lower())
sr = data.get('static_resources')
if sr is not None:
    with open('$WORK_DIR/col_resources_${fname}.json', 'w') as sf:
        json.dump(sr, sf)
print(len(cases))
" 2>/dev/null) || continue

  [[ -z "$cases" || "$cases" == "0" ]] && continue

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
  case_results_c=""
  for ((i=0; i<cases; i++)); do
    req_file="$WORK_DIR/col_req_${fname}_${i}.json"
    config_file="$WORK_DIR/col_config_${fname}"
    [[ -f "$req_file" && -f "$config_file" ]] || continue
    col_total=$((col_total + 1))
    if [[ -n "${CPP_RUN:-}" ]]; then
      cpp_col_total=$((cpp_col_total + 1))
    fi

    res_args=()
    if [[ -f "$WORK_DIR/col_resources_${fname}.json" ]]; then
      res_args=(-static-resources "$WORK_DIR/col_resources_${fname}.json")
    fi

    expect_error=""
    if [[ -f "$WORK_DIR/col_expect_error_${fname}_${i}.txt" ]]; then
      expect_error=$(cat "$WORK_DIR/col_expect_error_${fname}_${i}.txt")
    fi

    out_prefix="$WORK_DIR/col_out_${fname}_${i}"

    # Run all three engines in parallel
    run_engines_parallel "$config_file" "$req_file" "$out_prefix" "${res_args[@]}"

    go_rc=$(cat "${out_prefix}.go.rc")
    java_rc=$(cat "${out_prefix}.java.rc")

    if [[ -n "$expect_error" ]]; then
      if [[ "$go_rc" == "0" ]]; then
        fail "column-store expect_error but Go succeeded: $fname case $i"; case_results_j+="✗"; case_results_c+="✗"; continue
      fi
      if [[ "$java_rc" == "0" ]]; then
        fail "column-store expect_error but Java succeeded: $fname case $i"; case_results_j+="✗"; case_results_c+="✗"; continue
      fi

      go_err=$(cat "${out_prefix}.go.err")
      java_err=$(cat "${out_prefix}.java.err")

      err_ok=true
      [[ "$go_err" != *"$expect_error"* ]] && { fail "column-store Go error mismatch: $fname case $i"; err_ok=false; }
      [[ "$java_err" != *"$expect_error"* ]] && { fail "column-store Java error mismatch: $fname case $i"; err_ok=false; }

      # C++ error parity (if available)
      if [[ -n "${CPP_RUN:-}" ]]; then
        cpp_rc=$(cat "${out_prefix}.cpp.rc")
        if [[ "$cpp_rc" == "0" ]]; then
          fail "column-store expect_error but C++ succeeded: $fname case $i"; err_ok=false
        else
          cpp_err=$(cat "${out_prefix}.cpp.err")
          if [[ "$cpp_err" != *"$expect_error"* ]]; then
            fail "column-store C++ error mismatch: $fname case $i"; err_ok=false
          fi
        fi
      fi

      if [[ "$err_ok" == "true" ]]; then
        col_pass=$((col_pass + 1))
        case_results_j+="✓"
        if [[ -n "${CPP_RUN:-}" ]]; then
          cpp_col_pass=$((cpp_col_pass + 1))
          case_results_c+="✓"
        fi
      else
        case_results_j+="✗"
        case_results_c+="✗"
      fi
      continue
    fi

    if [[ "$go_rc" != "0" ]]; then
      fail "column-store Go failed: $fname case $i"; case_results_j+="✗"; continue
    fi
    if [[ "$java_rc" != "0" ]]; then
      fail "column-store Java failed: $fname case $i"; case_results_j+="✗"
    fi

    go_norm=$(cat "${out_prefix}.go.out" | normalize_json)
    java_norm=$(cat "${out_prefix}.java.out" | normalize_json)

    # Use set comparison when strict_order=false
    norm_fn=normalize_json
    if [[ -f "$WORK_DIR/col_strict_order_${fname}.txt" ]]; then
      so=$(cat "$WORK_DIR/col_strict_order_${fname}.txt")
      [[ "$so" == "false" ]] && norm_fn=normalize_json_set
    fi
    go_norm=$(cat "${out_prefix}.go.out" | $norm_fn)
    java_norm=$(cat "${out_prefix}.java.out" | $norm_fn)

    if [[ "$java_rc" == "0" ]]; then
      if [[ "$go_norm" == "$java_norm" ]]; then
        col_pass=$((col_pass + 1))
        case_results_j+="✓"
      else
        fail "column-store divergence (Go vs Java): $fname case $i"
        case_results_j+="✗"
      fi
    fi

    # C++ comparison (if available)
    if [[ -n "${CPP_RUN:-}" && -f "${out_prefix}.cpp.rc" ]]; then
      cpp_rc=$(cat "${out_prefix}.cpp.rc")
      if [[ "$cpp_rc" != "0" ]]; then
        fail "column-store C++ failed: $fname case $i"
        case_results_c+="✗"
      else
        cpp_norm=$(cat "${out_prefix}.cpp.out" | $norm_fn)
        if [[ "$go_norm" == "$cpp_norm" ]]; then
          cpp_col_pass=$((cpp_col_pass + 1))
          case_results_c+="✓"
        else
          fail "column-store divergence (Go vs C++): $fname case $i"
          diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$cpp_norm" | python3 -m json.tool) >&2 || true
          case_results_c+="✗"
        fi
      fi
    fi
  done
  cpp_tag=""
  [[ -n "${CPP_RUN:-}" ]] && cpp_tag="C${case_results_c}"
  echo "    $fname ($cases cases) [J${case_results_j}${cpp_tag}]"
done

if [[ $col_total -gt 0 && $col_pass -eq $col_total ]]; then
  pass "column-store execution parity Go vs Java ($col_pass/$col_total cases)"
elif [[ $col_total -eq 0 ]]; then
  pass "column-store execution parity Go vs Java (no cases, skipped)"
fi

if [[ -n "${CPP_RUN:-}" ]]; then
  if [[ $cpp_col_total -gt 0 && $cpp_col_pass -eq $cpp_col_total ]]; then
    pass "column-store execution parity Go vs C++ ($cpp_col_pass/$cpp_col_total cases)"
  elif [[ $cpp_col_total -eq 0 ]]; then
    pass "column-store execution parity Go vs C++ (no cases, skipped)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true

# TEST-2: row vs column cross-storage comparison.
# Compare Go-row output (Section 3) with Go-column output (Section 4)
# for every fixture case where both exist. Uses set-normalized comparison
# when strict_order=false.
row_col_pass=0
row_col_total=0
for fixture_file in "$FIXTURES_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")
  cases=$(python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
print(len(data.get('cases', [])))
" 2>/dev/null) || continue
  [[ -z "$cases" || "$cases" == "0" ]] && continue

  norm_fn=normalize_json
  if [[ -f "$WORK_DIR/col_strict_order_${fname}.txt" ]]; then
    so=$(cat "$WORK_DIR/col_strict_order_${fname}.txt")
    [[ "$so" == "false" ]] && norm_fn=normalize_json_set
  fi

  for i in $(seq 0 $((cases - 1))); do
    row_file="$WORK_DIR/out_${fname}_${i}.go.out"
    col_file="$WORK_DIR/col_out_${fname}_${i}.go.out"
    row_rc_file="$WORK_DIR/out_${fname}_${i}.go.rc"
    col_rc_file="$WORK_DIR/col_out_${fname}_${i}.go.rc"

    # Skip if either file doesn't exist (Section 3 may not have run yet in parallel mode)
    [[ -f "$row_file" && -f "$col_file" && -f "$row_rc_file" && -f "$col_rc_file" ]] || continue

    row_rc=$(cat "$row_rc_file")
    col_rc=$(cat "$col_rc_file")

    # Both must have same rc
    if [[ "$row_rc" != "$col_rc" ]]; then
      fail "row-vs-column rc mismatch (Go): $fname case $i (row=$row_rc col=$col_rc)"
      row_col_total=$((row_col_total + 1))
      continue
    fi

    # Skip error cases
    [[ "$row_rc" != "0" ]] && continue

    row_col_total=$((row_col_total + 1))
    row_norm=$(cat "$row_file" | $norm_fn)
    col_norm=$(cat "$col_file" | $norm_fn)
    if [[ "$row_norm" == "$col_norm" ]]; then
      row_col_pass=$((row_col_pass + 1))
    else
      fail "row-vs-column divergence (Go): $fname case $i"
    fi
  done
done

if [[ $row_col_total -gt 0 && $row_col_pass -eq $row_col_total ]]; then
  pass "row-vs-column Go output parity ($row_col_pass/$row_col_total cases)"
elif [[ $row_col_total -eq 0 ]]; then
  echo "    (row-vs-column comparison skipped: Section 3 outputs not available)"
fi
