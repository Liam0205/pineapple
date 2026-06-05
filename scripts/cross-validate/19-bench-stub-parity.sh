#!/usr/bin/env bash
# Section 19: bench-stub byte-equal reorder parity (audit H8)
#
# The bench stub family (`reorder_topn_boost`, `recall_feed_data`,
# `transform_redis_zrangebyscore`, ...) is gated behind per-runtime
# build/launch flags so production binaries never see them:
#   pine-go    → -tags pine_bench
#   pine-java  → -Dpine.bench=true
#   pine-cpp   → -DPINE_BUILD_BENCH_STUBS=ON
# Their primary purpose is throughput-only synthetic load, so the
# `realistic_for_you_*` fixtures are intentionally fixture-bare (no
# `cases/expected`). H8 closes the audit gap by pinning the one stub
# that does real work — `reorder_topn_boost` (FNV-1a salted hash sort
# + top-N front boost) — with a byte-equal `cases/expected` fixture,
# `fixtures/pipelines/reorder_topn_boost_parity.json` (`requires:
# ["bench"]`, so Section 03 skips it). The shape of this section
# mirrors 03 but uses the bench-enabled binaries built here.
#
# A new build_dir is used for cpp so we don't trample the
# PINE_BUILD_BENCH_STUBS=OFF objects produced by _prebuild.sh.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

echo
echo "==> [19/$TOTAL_SECTIONS] Bench-stub byte-equal reorder parity (Go/Java/C++ all bench-enabled)"

FIXTURE="$REPO_ROOT/fixtures/pipelines/reorder_topn_boost_parity.json"
if [[ ! -f "$FIXTURE" ]]; then
  fail "fixture missing: $FIXTURE"
  [[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
  return 0
fi

BENCH_WORK="$WORK_DIR/bench"
mkdir -p "$BENCH_WORK"
BENCH_LOG="$BENCH_WORK/build.log"

echo "    Building bench-enabled Go binary (-tags pine_bench)..."
if ! (cd "$REPO_ROOT/pine-go" && go build -tags pine_bench -o "$BENCH_WORK/pineapple-run-bench" ./cmd/pineapple-run/) >"$BENCH_LOG" 2>&1; then
  fail "Go bench build failed (last 30 lines $BENCH_LOG):"
  tail -n 30 "$BENCH_LOG" >&2 || true
  [[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
  return 0
fi

echo "    Building bench-enabled C++ binary (-DPINE_BUILD_BENCH_STUBS=ON)..."
CPP_BENCH_BUILD="$REPO_ROOT/pine-cpp/build-bench"
mkdir -p "$CPP_BENCH_BUILD"
if ! (cd "$CPP_BENCH_BUILD" \
        && cmake .. -DCMAKE_BUILD_TYPE=Release -DPINE_BUILD_BENCH_STUBS=ON 2>&1 \
        && make -j2 pineapple-run 2>&1) >>"$BENCH_LOG" 2>&1; then
  fail "C++ bench build failed (last 50 lines $BENCH_LOG):"
  tail -n 50 "$BENCH_LOG" >&2 || true
  [[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
  return 0
fi
cp "$CPP_BENCH_BUILD/pineapple-run" "$BENCH_WORK/pineapple-run-bench-cpp"

# Java: existing classes already include BenchStubs; we only need the
# -Dpine.bench=true system property at launch (AllOperators.java:305).
# No extra compile step required.

# Extract config + per-case requests/expected.
python3 -c "
import json
with open('$FIXTURE') as f:
    data = json.load(f)
cfg = data.get('config', {})
with open('$BENCH_WORK/config.json', 'w') as cf:
    json.dump(cfg, cf)
cases = data.get('cases', [])
for i, c in enumerate(cases):
    with open('$BENCH_WORK/req_%d.json' % i, 'w') as rf:
        json.dump(c.get('request', {}), rf)
    with open('$BENCH_WORK/expected_%d.json' % i, 'w') as ef:
        json.dump(c.get('expected', {}), ef)
print(len(cases))
" >"$BENCH_WORK/case_count.txt"

cases=$(cat "$BENCH_WORK/case_count.txt")
if [[ -z "$cases" || "$cases" == "0" ]]; then
  fail "fixture has no cases"
  [[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
  return 0
fi

case_results_g=""
case_results_j=""
case_results_c=""
total_pass=0
total_runs=0

for ((i=0; i<cases; i++)); do
  cfg_file="$BENCH_WORK/config.json"
  req_file="$BENCH_WORK/req_${i}.json"
  exp_file="$BENCH_WORK/expected_${i}.json"
  out_g="$BENCH_WORK/out_${i}.go.json"
  out_j="$BENCH_WORK/out_${i}.java.json"
  out_c="$BENCH_WORK/out_${i}.cpp.json"
  err_g="$BENCH_WORK/out_${i}.go.err"
  err_j="$BENCH_WORK/out_${i}.java.err"
  err_c="$BENCH_WORK/out_${i}.cpp.err"

  # Go (bench-enabled)
  if "$BENCH_WORK/pineapple-run-bench" -config "$cfg_file" -request "$req_file" >"$out_g" 2>"$err_g"; then
    go_rc=0
  else
    go_rc=$?
  fi

  # Java (system-property gated)
  if java_run -Dpine.bench=true page.liam.pine.RunCli -config "$cfg_file" -request "$req_file" >"$out_j" 2>"$err_j"; then
    java_rc=0
  else
    java_rc=$?
  fi

  # C++ (bench-built)
  if "$BENCH_WORK/pineapple-run-bench-cpp" -config "$cfg_file" -request "$req_file" >"$out_c" 2>"$err_c"; then
    cpp_rc=0
  else
    cpp_rc=$?
  fi

  exp_norm=$(cat "$exp_file" | normalize_json)

  for tag in g j c; do
    case "$tag" in
      g) rc="$go_rc";   out_file="$out_g";   err_file="$err_g";   label=Go ;;
      j) rc="$java_rc"; out_file="$out_j";   err_file="$err_j";   label=Java ;;
      c) rc="$cpp_rc";  out_file="$out_c";   err_file="$err_c";   label=C++ ;;
    esac
    total_runs=$((total_runs + 1))
    if [[ "$rc" != "0" ]]; then
      fail "$label execution failed (case $i, rc=$rc):"
      tail -n 20 "$err_file" >&2 || true
      eval "case_results_${tag}+=\"✗\""
      continue
    fi
    got_norm=$(cat "$out_file" | normalize_json)
    if [[ "$got_norm" == "$exp_norm" ]]; then
      total_pass=$((total_pass + 1))
      eval "case_results_${tag}+=\"✓\""
    else
      fail "$label diverged from expected (case $i)"
      diff <(echo "$got_norm" | python3 -m json.tool) <(echo "$exp_norm" | python3 -m json.tool) >&2 || true
      eval "case_results_${tag}+=\"✗\""
    fi
  done
done

echo "    reorder_topn_boost_parity.json ($cases cases) [G${case_results_g}J${case_results_j}C${case_results_c}]"

if [[ $total_runs -gt 0 && $total_pass -eq $total_runs ]]; then
  pass "bench-stub byte-equal reorder parity (Go/Java/C++ all match expected, $total_pass/$total_runs)"
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
