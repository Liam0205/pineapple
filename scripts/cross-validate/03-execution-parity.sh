#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_parallel.sh"

# ---------- 3. Dual-engine execution parity ----------
echo
echo "==> [3/$TOTAL_SECTIONS] Execution parity (Go vs Java vs Python vs C++ on same config+request)"

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"
exec_pass=0
exec_total=0
py_exec_pass=0
py_exec_total=0
cpp_exec_pass=0
cpp_exec_total=0

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
    with open('$WORK_DIR/req_${fname}_' + str(i) + '.json', 'w') as rf:
        json.dump(req, rf)
    ee = c.get('expect_error', '')
    with open('$WORK_DIR/expect_error_${fname}_' + str(i) + '.txt', 'w') as ef:
        ef.write(ee)
sr = data.get('static_resources')
if sr is not None:
    with open('$WORK_DIR/resources_${fname}.json', 'w') as sf:
        json.dump(sr, sf)
print(len(cases))
" 2>/dev/null) || continue

  [[ -z "$cases" || "$cases" == "0" ]] && continue

  python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
with open('$WORK_DIR/config_${fname}', 'w') as cf:
    json.dump(data.get('config', {}), cf)
" 2>/dev/null || continue

  case_results_j=""
  case_results_p=""
  case_results_c=""
  for ((i=0; i<cases; i++)); do
    req_file="$WORK_DIR/req_${fname}_${i}.json"
    config_file="$WORK_DIR/config_${fname}"
    [[ -f "$req_file" && -f "$config_file" ]] || continue
    exec_total=$((exec_total + 1))
    py_exec_total=$((py_exec_total + 1))
    if [[ -n "${CPP_RUN:-}" ]]; then
      cpp_exec_total=$((cpp_exec_total + 1))
    fi

    res_args=()
    if [[ -f "$WORK_DIR/resources_${fname}.json" ]]; then
      res_args=(-static-resources "$WORK_DIR/resources_${fname}.json")
    fi

    expect_error=""
    if [[ -f "$WORK_DIR/expect_error_${fname}_${i}.txt" ]]; then
      expect_error=$(cat "$WORK_DIR/expect_error_${fname}_${i}.txt")
    fi

    out_prefix="$WORK_DIR/out_${fname}_${i}"

    # Run all three engines in parallel
    run_engines_parallel "$config_file" "$req_file" "$out_prefix" "${res_args[@]}"

    go_rc=$(cat "${out_prefix}.go.rc")
    java_rc=$(cat "${out_prefix}.java.rc")
    py_rc=$(cat "${out_prefix}.py.rc")

    if [[ -n "$expect_error" ]]; then
      # All should fail
      if [[ "$go_rc" == "0" ]]; then
        fail "expect_error but Go succeeded: $fname case $i"; case_results_j+="✗"; case_results_p+="✗"; case_results_c+="✗"; continue
      fi
      if [[ "$java_rc" == "0" ]]; then
        fail "expect_error but Java succeeded: $fname case $i"; case_results_j+="✗"; case_results_p+="✗"; case_results_c+="✗"; continue
      fi
      if [[ "$py_rc" == "0" ]]; then
        fail "expect_error but Python succeeded: $fname case $i"; case_results_j+="✗"; case_results_p+="✗"; case_results_c+="✗"; continue
      fi

      go_err=$(cat "${out_prefix}.go.err")
      java_err=$(cat "${out_prefix}.java.err")
      py_err=$(cat "${out_prefix}.py.err")

      err_ok=true
      if [[ "$go_err" != *"$expect_error"* ]]; then
        fail "Go error mismatch: $fname case $i (want '$expect_error')"; err_ok=false
      fi
      if [[ "$java_err" != *"$expect_error"* ]]; then
        fail "Java error mismatch: $fname case $i (want '$expect_error')"; err_ok=false
      fi
      if [[ "$py_err" != *"$expect_error"* ]]; then
        fail "Python error mismatch: $fname case $i (want '$expect_error')"; err_ok=false
      fi

      # C++ error parity (if available)
      if [[ -n "${CPP_RUN:-}" ]]; then
        cpp_rc=$(cat "${out_prefix}.cpp.rc")
        if [[ "$cpp_rc" == "0" ]]; then
          fail "expect_error but C++ succeeded: $fname case $i"; err_ok=false
        else
          cpp_err=$(cat "${out_prefix}.cpp.err")
          if [[ "$cpp_err" != *"$expect_error"* ]]; then
            fail "C++ error mismatch: $fname case $i (want '$expect_error')"; err_ok=false
          fi
        fi
      fi

      if [[ "$err_ok" == "true" ]]; then
        exec_pass=$((exec_pass + 1))
        py_exec_pass=$((py_exec_pass + 1))
        case_results_j+="✓"
        case_results_p+="✓"
        if [[ -n "${CPP_RUN:-}" ]]; then
          cpp_exec_pass=$((cpp_exec_pass + 1))
          case_results_c+="✓"
        fi
      else
        case_results_j+="✗"
        case_results_p+="✗"
        case_results_c+="✗"
      fi
      continue
    fi

    # Normal case: all should succeed
    if [[ "$go_rc" != "0" ]]; then
      fail "execution Go failed: $fname case $i"; case_results_j+="✗"; case_results_p+="✗"; continue
    fi
    if [[ "$java_rc" != "0" ]]; then
      fail "execution Java failed: $fname case $i"; case_results_j+="✗"
    fi

    go_norm=$(cat "${out_prefix}.go.out" | normalize_json)
    java_norm=$(cat "${out_prefix}.java.out" | normalize_json)

    if [[ "$java_rc" == "0" ]]; then
      if [[ "$go_norm" == "$java_norm" ]]; then
        exec_pass=$((exec_pass + 1))
        case_results_j+="✓"
      else
        fail "execution divergence (Go vs Java): $fname case $i"
        diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$java_norm" | python3 -m json.tool) >&2 || true
        case_results_j+="✗"
      fi
    fi

    if [[ "$py_rc" != "0" ]]; then
      fail "execution Python failed: $fname case $i"; case_results_p+="✗"; continue
    fi

    py_norm=$(cat "${out_prefix}.py.out" | normalize_json)

    if [[ "$go_norm" == "$py_norm" ]]; then
      py_exec_pass=$((py_exec_pass + 1))
      case_results_p+="✓"
    else
      fail "execution divergence (Go vs Python): $fname case $i"
      diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$py_norm" | python3 -m json.tool) >&2 || true
      case_results_p+="✗"
    fi

    # C++ comparison (if available)
    if [[ -n "${CPP_RUN:-}" && -f "${out_prefix}.cpp.rc" ]]; then
      cpp_rc=$(cat "${out_prefix}.cpp.rc")
      if [[ "$cpp_rc" != "0" ]]; then
        fail "execution C++ failed: $fname case $i"
        case_results_c+="✗"
      else
        cpp_norm=$(cat "${out_prefix}.cpp.out" | normalize_json)
        if [[ "$go_norm" == "$cpp_norm" ]]; then
          cpp_exec_pass=$((cpp_exec_pass + 1))
          case_results_c+="✓"
        else
          fail "execution divergence (Go vs C++): $fname case $i"
          diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$cpp_norm" | python3 -m json.tool) >&2 || true
          case_results_c+="✗"
        fi
      fi
    fi
  done
  cpp_tag=""
  [[ -n "${CPP_RUN:-}" ]] && cpp_tag="C${case_results_c}"
  echo "    $fname ($cases cases) [J${case_results_j}P${case_results_p}${cpp_tag}]"
done

if [[ $exec_total -gt 0 && $exec_pass -eq $exec_total ]]; then
  pass "execution parity Go vs Java ($exec_pass/$exec_total cases)"
elif [[ $exec_total -eq 0 ]]; then
  pass "execution parity Go vs Java (no pipeline fixture cases found, skipped)"
fi

if [[ $py_exec_total -gt 0 && $py_exec_pass -eq $py_exec_total ]]; then
  pass "execution parity Go vs Python ($py_exec_pass/$py_exec_total cases)"
elif [[ $py_exec_total -eq 0 ]]; then
  pass "execution parity Go vs Python (no pipeline fixture cases found, skipped)"
fi

if [[ -n "${CPP_RUN:-}" ]]; then
  if [[ $cpp_exec_total -gt 0 && $cpp_exec_pass -eq $cpp_exec_total ]]; then
    pass "execution parity Go vs C++ ($cpp_exec_pass/$cpp_exec_total cases)"
  elif [[ $cpp_exec_total -eq 0 ]]; then
    pass "execution parity Go vs C++ (no pipeline fixture cases found, skipped)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
