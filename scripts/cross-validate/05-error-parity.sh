#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 5. Error parity ----------
echo
echo "==> [5/$TOTAL_SECTIONS] Error parity (Go vs Java vs Python on invalid configs)"

ERRORS_DIR="$REPO_ROOT/fixtures/errors"
err_pass=0
err_total=0
py_err_pass=0
py_err_total=0

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
  py_ok=true

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

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
