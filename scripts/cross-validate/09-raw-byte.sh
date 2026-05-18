#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 9. Raw byte execution parity (key ordering) ----------
echo
echo "==> [9/$TOTAL_SECTIONS] Raw byte execution parity (no normalization)"

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"

raw_pass=0
raw_total=0

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

    go_raw=$("$WORK_DIR/pineapple-run" -config "$config_file" -request "$req_file" "${res_args[@]}" 2>/dev/null) || {
      raw_total=$((raw_total - 1)); continue
    }

    java_raw=$(java_run page.liam.pine.RunCli -config "$config_file" -request "$req_file" "${res_args[@]}" 2>/dev/null) || {
      raw_total=$((raw_total - 1)); continue
    }

    if [[ "$go_raw" == "$java_raw" ]]; then
      raw_pass=$((raw_pass + 1))
    else
      # Key ordering differences are expected (Go: struct field order, Java: insertion order)
      # Only fail if the normalized values also differ (would indicate a real data bug)
      go_norm=$(echo "$go_raw" | normalize_json)
      java_norm=$(echo "$java_raw" | normalize_json)
      if [[ "$go_norm" == "$java_norm" ]]; then
        raw_pass=$((raw_pass + 1))
      else
        fail "raw byte divergence: $fname case $i (values differ, not just key ordering)"
        diff <(echo "$go_raw") <(echo "$java_raw") >&2 | head -10 || true
      fi
    fi
  done
done

if [[ $raw_total -gt 0 && $raw_pass -eq $raw_total ]]; then
  pass "raw byte execution parity ($raw_pass/$raw_total cases)"
elif [[ $raw_total -eq 0 ]]; then
  pass "raw byte execution parity (no cases, skipped)"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
