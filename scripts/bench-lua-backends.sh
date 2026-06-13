#!/usr/bin/env bash
# wangshu vs gopher-lua 后端对比 benchmark。
#
# 在同机同时段串行连跑两个 Lua 后端(由 build tag 选择),用 benchstat 给出
# 统计显著的 delta。calibrated fixture 是后端选型的唯一裁判(见
# llmdoc/guides/benchmark-hygiene.md);默认跑 BenchmarkCalibrated,也可用
# -bench 覆盖为 synthetic 的 BenchmarkIsolated / BenchmarkLuaVsGo。
#
# 用法:
#   scripts/bench-lua-backends.sh [-bench PATTERN] [-count N] [-procs N] [-serial]
#
# 选项:
#   -bench PATTERN  传给 go test -bench 的正则(默认 BenchmarkCalibrated)
#   -count N        每后端采样次数(默认 10)
#   -procs N        GOMAXPROCS(默认 4;对照 synthetic 同口径)。仅作用于
#                   GOMAXPROCS,bench 不分 cpu 维度——非 serial 模式下不传 -cpu,
#                   依赖 BenchmarkCalibrated/Isolated 内部不调用 b.RunParallel
#                   保持单 cpu 列输出,避免 benchstat 看到混合维度。
#   -serial         等价 -procs 1,额外加 -cpu=1 显式锁死单 cpu 列,去除 DAG
#                   调度抖动,隔离 Lua 路径
#   -keep DIR       结果输出目录(默认临时目录,跑完打印路径)
#
# 前置依赖:
#   - benchstat: go install golang.org/x/perf/cmd/benchstat@latest
#   - pine_bench build tag 下的 stub 算子(operators/bench/),让 calibrated
#     fixture 无需真实 MySQL/Redis/Datahub 即可 in-process 跑。
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

# benchstat 解析:优先 PATH,回退 GOPATH/bin。
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

# ─── 跑前卫生检查(见 benchmark-hygiene.md) ──────────────────────────────
echo "==> Pre-flight:"
uptime
if pgrep -af 'go test.*-bench' | grep -vq -e grep -e "$$"; then
  echo "  ! 有其他 go bench 在跑,先清机再来(bench 不可并行)" >&2
  exit 1
fi
echo

COMMON_FLAGS=(-run='^$' -bench="$BENCH_PATTERN" -benchmem -count="$COUNT")
[[ "$PROCS" == 1 ]] && COMMON_FLAGS+=(-cpu=1)

# ─── 后端 A: gopher-lua(opt-in lua_gopher tag,作为 benchstat 基线) ──────────
echo "==> [1/2] gopher-lua (GOMAXPROCS=$PROCS, count=$COUNT, bench=$BENCH_PATTERN)"
( cd "$BENCH_DIR" && GOMAXPROCS="$PROCS" go test -tags='pine_bench lua_gopher' "${COMMON_FLAGS[@]}" ./... ) \
  | tee "$GOPHER_OUT" | tail -3
echo

# ─── 后端 B: wangshu(默认 tag) ─────────────────────────────────────────────
echo "==> [2/2] wangshu (GOMAXPROCS=$PROCS, count=$COUNT, bench=$BENCH_PATTERN)"
( cd "$BENCH_DIR" && GOMAXPROCS="$PROCS" go test -tags=pine_bench "${COMMON_FLAGS[@]}" ./... ) \
  | tee "$WANGSHU_OUT" | tail -3
echo

# ─── 跑后卫生检查 + 统计对比 ──────────────────────────────────────────────
echo "==> Post-flight:"
uptime
echo
echo "==> benchstat (base=gopher-lua, vs=wangshu):"
"$BENCHSTAT" "$GOPHER_OUT" "$WANGSHU_OUT"
echo
echo "==> Raw results kept in: $OUTDIR"
