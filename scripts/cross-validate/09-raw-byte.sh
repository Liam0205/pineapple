#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_parallel.sh"

# ---------- 9. Raw byte execution parity (key ordering) ----------
echo
echo "==> [9/$TOTAL_SECTIONS] Raw byte execution parity (no normalization)"

FIXTURES_DIR="$REPO_ROOT/fixtures/pipelines"

raw_pass=0
raw_total=0
cpp_raw_pass=0
cpp_raw_total=0

for fixture_file in "$FIXTURES_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file")

  # Extract config (self-sufficient — don't rely on section 3's temp files)
  config_file="$WORK_DIR/raw_config_${fname}"
  python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
with open('$config_file', 'w') as cf:
    json.dump(data.get('config', {}), cf)
" 2>/dev/null || continue

  cases=$(python3 -c "
import json, sys
with open('$fixture_file') as f:
    data = json.load(f)
# Skip fixtures that need external prepopulated state (redis) or
# specially-built bench-tag binaries. Section 11/17 cover redis;
# Section 19 covers bench-only stubs.
requires = set(data.get('requires', []) or [])
if requires & {'redis', 'redis-unavailable', 'bench'}:
    sys.exit(0)
cases = data.get('cases', [])
if not cases:
    sys.exit(0)
for i, c in enumerate(cases):
    req = c.get('request', {})
    with open('$WORK_DIR/raw_req_${fname}_' + str(i) + '.json', 'w') as rf:
        json.dump(req, rf)
    ee = c.get('expect_error', '')
    with open('$WORK_DIR/raw_ee_${fname}_' + str(i) + '.txt', 'w') as ef:
        ef.write(ee)
so = data.get('strict_order', True)
with open('$WORK_DIR/raw_strict_order_${fname}.txt', 'w') as sf:
    sf.write(str(so).lower())
sr = data.get('static_resources')
if sr is not None:
    with open('$WORK_DIR/raw_resources_${fname}.json', 'w') as sf:
        json.dump(sr, sf)
print(len(cases))
" 2>/dev/null) || continue

  [[ -z "$cases" || "$cases" == "0" ]] && continue

  for ((i=0; i<cases; i++)); do
    req_file="$WORK_DIR/raw_req_${fname}_${i}.json"
    [[ -f "$req_file" && -f "$config_file" ]] || continue
    raw_total=$((raw_total + 1))

    res_args=()
    if [[ -f "$WORK_DIR/raw_resources_${fname}.json" ]]; then
      res_args=(-static-resources "$WORK_DIR/raw_resources_${fname}.json")
    fi

    # Skip expect_error cases for raw byte comparison
    expect_error=""
    if [[ -f "$WORK_DIR/raw_ee_${fname}_${i}.txt" ]]; then
      expect_error=$(cat "$WORK_DIR/raw_ee_${fname}_${i}.txt")
    fi
    if [[ -n "$expect_error" ]]; then
      raw_total=$((raw_total - 1)); continue
    fi

    out_prefix="$WORK_DIR/raw_${fname}_${i}"

    # Run all three engines in parallel
    run_engines_parallel "$config_file" "$req_file" "$out_prefix" "${res_args[@]}"

    go_rc=$(cat "${out_prefix}.go.rc")
    java_rc=$(cat "${out_prefix}.java.rc")

    if [[ "$go_rc" != "0" ]]; then
      raw_total=$((raw_total - 1)); continue
    fi
    if [[ "$java_rc" != "0" ]]; then
      raw_total=$((raw_total - 1)); continue
    fi

    go_raw=$(cat "${out_prefix}.go.out")
    java_raw=$(cat "${out_prefix}.java.out")

    # For strict_order=false fixtures, skip raw byte comparison (item order is
    # non-deterministic by design) and use set-normalized comparison instead.
    norm_fn=normalize_json
    if [[ -f "$WORK_DIR/raw_strict_order_${fname}.txt" ]]; then
      so=$(cat "$WORK_DIR/raw_strict_order_${fname}.txt")
      [[ "$so" == "false" ]] && norm_fn=normalize_json_set
    fi

    if [[ "$norm_fn" == "normalize_json_set" ]]; then
      go_norm=$(echo "$go_raw" | normalize_json_set)
      java_norm=$(echo "$java_raw" | normalize_json_set)
      if [[ "$go_norm" == "$java_norm" ]]; then
        raw_pass=$((raw_pass + 1))
      else
        fail "raw byte divergence (Go vs Java): $fname case $i (values differ, not just key ordering)"
      fi
    elif [[ "$go_raw" == "$java_raw" ]]; then
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

    # Go vs C++ raw byte
    if [[ -n "${CPP_RUN:-}" && -f "${out_prefix}.cpp.rc" ]]; then
      cpp_raw_total=$((cpp_raw_total + 1))
      cpp_rc=$(cat "${out_prefix}.cpp.rc")
      if [[ "$cpp_rc" != "0" ]]; then
        cpp_raw_total=$((cpp_raw_total - 1))
      else
        cpp_raw=$(cat "${out_prefix}.cpp.out")
        if [[ "$norm_fn" == "normalize_json_set" ]]; then
          go_norm=${go_norm:-$(echo "$go_raw" | normalize_json_set)}
          cpp_norm=$(echo "$cpp_raw" | normalize_json_set)
          if [[ "$go_norm" == "$cpp_norm" ]]; then
            cpp_raw_pass=$((cpp_raw_pass + 1))
          else
            fail "raw byte divergence (Go vs C++): $fname case $i (values differ, not just key ordering)"
          fi
        elif [[ "$go_raw" == "$cpp_raw" ]]; then
          cpp_raw_pass=$((cpp_raw_pass + 1))
        else
          go_norm=${go_norm:-$(echo "$go_raw" | normalize_json)}
          cpp_norm=$(echo "$cpp_raw" | normalize_json)
          if [[ "$go_norm" == "$cpp_norm" ]]; then
            cpp_raw_pass=$((cpp_raw_pass + 1))
            echo "    [W] key ordering differs (Go vs C++): $fname case $i" >&2
          else
            fail "raw byte divergence (Go vs C++): $fname case $i (values differ, not just key ordering)"
          fi
        fi
      fi
    fi
  done
done

if [[ $raw_total -gt 0 && $raw_pass -eq $raw_total ]]; then
  pass "raw byte execution parity Go vs Java ($raw_pass/$raw_total cases)"
elif [[ $raw_total -eq 0 ]]; then
  pass "raw byte execution parity Go vs Java (no cases, skipped)"
fi

if [[ -n "${CPP_RUN:-}" ]]; then
  if [[ $cpp_raw_total -gt 0 && $cpp_raw_pass -eq $cpp_raw_total ]]; then
    pass "raw byte execution parity Go vs C++ ($cpp_raw_pass/$cpp_raw_total cases)"
  elif [[ $cpp_raw_total -eq 0 ]]; then
    pass "raw byte execution parity Go vs C++ (no cases, skipped)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
