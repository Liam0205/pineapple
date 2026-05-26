#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 5. Error parity ----------
echo
echo "==> [5/$TOTAL_SECTIONS] Error parity (Go vs Java vs Python vs C++ on invalid configs)"

ERRORS_DIR="$REPO_ROOT/fixtures/errors"
err_pass=0
err_total=0
py_err_pass=0
py_err_total=0
cpp_err_pass=0
cpp_err_total=0

for fixture_file in "$ERRORS_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")
  err_total=$((err_total + 1))
  py_err_total=$((py_err_total + 1))

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

  # Python should also fail
  py_err=$(py_run pine.cli.run -config "$config_file" -request "$req_file" 2>&1) && {
    fail "error parity: Python succeeded unexpectedly: $fname"
    py_err=""
  }

  # C++ should also fail (if available)
  cpp_err=""
  if [[ -n "${CPP_RUN:-}" ]]; then
    cpp_err_total=$((cpp_err_total + 1))
    cpp_err=$("$CPP_RUN" -config "$config_file" -request "$req_file" 2>&1) && {
      fail "error parity: C++ succeeded unexpectedly: $fname"
      cpp_err=""
    }
  fi

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

  # wrapping_exact: byte-exact substring required for the listed engines.
  # Locks the canonical `pine: execution error in operator "X":` prefix so
  # any engine that diverges from the canonical format is caught.
  wrapping_exact=$(python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
print(data.get('expected_error', {}).get('wrapping_exact', ''))
" 2>/dev/null)
  wrapping_engines=$(python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
print(' '.join(data.get('expected_error', {}).get('wrapping_exact_engines', [])))
" 2>/dev/null)

  # Verify both errors contain expected substring
  go_ok=true
  java_ok=true
  py_ok=true
  cpp_ok=true

  if [[ -n "$expected_contains" ]]; then
    if ! echo "$go_err" | grep -qi "$expected_contains"; then
      go_ok=false
    fi
    if ! echo "$java_err" | grep -qi "$expected_contains"; then
      java_ok=false
    fi
    if [[ -n "$py_err" ]]; then
      if ! echo "$py_err" | grep -qi "$expected_contains"; then
        py_ok=false
      fi
    else
      py_ok=false
    fi
    if [[ -n "${CPP_RUN:-}" ]]; then
      if [[ -n "$cpp_err" ]]; then
        if ! echo "$cpp_err" | grep -qi "$expected_contains"; then
          cpp_ok=false
        fi
      else
        cpp_ok=false
      fi
    fi
  fi

  # Byte-exact wrapping check (case-sensitive fixed-string match). Only
  # enforced for engines listed in wrapping_exact_engines so we can opt
  # individual engines in as their format converges to the canonical one.
  if [[ -n "$wrapping_exact" ]]; then
    for eng in $wrapping_engines; do
      case "$eng" in
        go)
          if ! echo "$go_err" | grep -qF "$wrapping_exact" >/dev/null; then
            go_ok=false
          fi
          ;;
        java)
          if ! echo "$java_err" | grep -qF "$wrapping_exact" >/dev/null; then
            java_ok=false
          fi
          ;;
        python|py)
          if [[ -z "$py_err" ]] || ! echo "$py_err" | grep -qF "$wrapping_exact" >/dev/null; then
            py_ok=false
          fi
          ;;
        cpp|c++)
          if [[ -n "${CPP_RUN:-}" ]]; then
            if [[ -z "$cpp_err" ]] || ! echo "$cpp_err" | grep -qF "$wrapping_exact" >/dev/null; then
              cpp_ok=false
            fi
          fi
          ;;
      esac
    done
  fi

  if [[ "$go_ok" == "true" && "$java_ok" == "true" ]]; then
    err_pass=$((err_pass + 1))
    echo "    [$err_total] $fname → Go & Java failed correctly"
  else
    fail "error parity (Go vs Java): $fname (go_match=$go_ok, java_match=$java_ok)"
    echo "      Go:   $go_err" | head -3 >&2
    echo "      Java: $java_err" | head -3 >&2
  fi

  if [[ "$go_ok" == "true" && "$py_ok" == "true" ]]; then
    py_err_pass=$((py_err_pass + 1))
    echo "    [$err_total] $fname → Go & Python failed correctly"
  else
    fail "error parity (Go vs Python): $fname (go_match=$go_ok, py_match=$py_ok)"
    if [[ -n "$py_err" ]]; then
      echo "      Python: $py_err" | head -3 >&2
    fi
  fi

  if [[ -n "${CPP_RUN:-}" ]]; then
    if [[ "$go_ok" == "true" && "$cpp_ok" == "true" ]]; then
      cpp_err_pass=$((cpp_err_pass + 1))
      echo "    [$err_total] $fname → Go & C++ failed correctly"
    else
      fail "error parity (Go vs C++): $fname (go_match=$go_ok, cpp_match=$cpp_ok)"
      if [[ -n "$cpp_err" ]]; then
        echo "      C++: $cpp_err" | head -3 >&2
      fi
    fi
  fi
done

if [[ $err_total -gt 0 && $err_pass -eq $err_total ]]; then
  pass "error parity Go vs Java ($err_pass/$err_total fixtures)"
elif [[ $err_total -eq 0 ]]; then
  pass "error parity Go vs Java (no error fixtures found, skipped)"
fi

if [[ $py_err_total -gt 0 && $py_err_pass -eq $py_err_total ]]; then
  pass "error parity Go vs Python ($py_err_pass/$py_err_total fixtures)"
elif [[ $py_err_total -eq 0 ]]; then
  pass "error parity Go vs Python (no error fixtures found, skipped)"
fi

if [[ -n "${CPP_RUN:-}" ]]; then
  if [[ $cpp_err_total -gt 0 && $cpp_err_pass -eq $cpp_err_total ]]; then
    pass "error parity Go vs C++ ($cpp_err_pass/$cpp_err_total fixtures)"
  elif [[ $cpp_err_total -eq 0 ]]; then
    pass "error parity Go vs C++ (no error fixtures found, skipped)"
  fi
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
