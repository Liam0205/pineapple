#!/usr/bin/env bash
# wangshu vs gopher-lua backend benchmark.
#
# Runs the two Lua backends (selected by build tag) back to back on the same
# machine in the same time window, then reports statistically significant deltas
# with benchstat. The calibrated fixture is the only reference for choosing a
# backend (see llmdoc/guides/benchmark-hygiene.md); BenchmarkCalibrated runs by
# default, and -bench can switch to the synthetic BenchmarkIsolated / BenchmarkLuaVsGo.
#
# Usage:
#   scripts/bench-lua-backends.sh [-bench PATTERN] [-count N] [-procs N] [-serial]
#
# Options:
#   -bench PATTERN  Regex passed to go test -bench (default BenchmarkCalibrated)
#   -count N        Samples per backend (default 10)
#   -procs N        GOMAXPROCS (default 4, same as the synthetic runs). Only sets
#                   GOMAXPROCS; the benchmark has no cpu dimension — -cpu is not
#                   passed outside serial mode, relying on BenchmarkCalibrated/Isolated
#                   not calling b.RunParallel to keep a single cpu column, so
#                   benchstat never sees mixed dimensions.
#   -serial         Same as -procs 1, plus -cpu=1 to pin a single cpu column
#                   explicitly, removing DAG scheduling jitter and isolating the Lua path
#   -keep DIR       Output directory (default: a temp dir, printed when done)
#
# Prerequisites:
#   - benchstat: go install golang.org/x/perf/cmd/benchstat@latest
#   - the stub operators behind the pine_bench build tag (operators/bench/), so the
#     calibrated fixture runs in-process without a real MySQL/Redis/Datahub.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BENCH_DIR="$REPO_ROOT/pine-go/benchmarks"

BENCH_PATTERN="BenchmarkCalibrated"
COUNT=10
PROCS=4
OUTDIR=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -bench)  BENCH_PATTERN="$2"; shift 2 ;;
    -count)  COUNT="$2"; shift 2 ;;
    -procs)  PROCS="$2"; shift 2 ;;
    -serial) PROCS=1; shift ;;
    -keep)   OUTDIR="$2"; shift 2 ;;
    *) echo "Unknown arg: $1" >&2; exit 1 ;;
  esac
done

# Locate benchstat: PATH first, then fall back to GOPATH/bin.
BENCHSTAT="$(command -v benchstat || true)"
if [[ -z "$BENCHSTAT" ]]; then
  CAND="$(go env GOPATH)/bin/benchstat"
  [[ -x "$CAND" ]] && BENCHSTAT="$CAND"
fi
if [[ -z "$BENCHSTAT" ]]; then
  echo "Error: benchstat not found. Install: go install golang.org/x/perf/cmd/benchstat@latest" >&2
  exit 1
fi

[[ -z "$OUTDIR" ]] && OUTDIR="$(mktemp -d /tmp/bench-lua-backends.XXXXXX)"
mkdir -p "$OUTDIR"
GOPHER_OUT="$OUTDIR/gopher.txt"
WANGSHU_OUT="$OUTDIR/wangshu.txt"

# ─── Pre-run environment check (see benchmark-hygiene.md) ─────────────────
echo "==> Pre-flight:"
uptime
if pgrep -af 'go test.*-bench' | grep -vq -e grep -e "$$"; then
  echo "  ! 有其他 go bench 在跑,先清机再来(bench 不可并行)" >&2
  exit 1
fi
echo

COMMON_FLAGS=(-run='^$' -bench="$BENCH_PATTERN" -benchmem -count="$COUNT")
[[ "$PROCS" == 1 ]] && COMMON_FLAGS+=(-cpu=1)

# ─── Backend A: gopher-lua (opt-in lua_gopher tag, the benchstat baseline) ──
echo "==> [1/2] gopher-lua (GOMAXPROCS=$PROCS, count=$COUNT, bench=$BENCH_PATTERN)"
( cd "$BENCH_DIR" && GOMAXPROCS="$PROCS" go test -tags='pine_bench lua_gopher' "${COMMON_FLAGS[@]}" ./... ) \
  | tee "$GOPHER_OUT" | tail -3
echo

# ─── Backend B: wangshu (default tag) ──────────────────────────────────────
echo "==> [2/2] wangshu (GOMAXPROCS=$PROCS, count=$COUNT, bench=$BENCH_PATTERN)"
( cd "$BENCH_DIR" && GOMAXPROCS="$PROCS" go test -tags=pine_bench "${COMMON_FLAGS[@]}" ./... ) \
  | tee "$WANGSHU_OUT" | tail -3
echo

# ─── Post-run environment check + statistical comparison ──────────────────
echo "==> Post-flight:"
uptime
echo
echo "==> benchstat (base=gopher-lua, vs=wangshu):"
"$BENCHSTAT" "$GOPHER_OUT" "$WANGSHU_OUT"
echo
echo "==> Raw results kept in: $OUTDIR"
