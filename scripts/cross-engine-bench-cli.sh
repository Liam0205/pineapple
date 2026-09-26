#!/usr/bin/env bash
# Cross-engine CLI benchmark — a simple version that calls each CLI directly to
# measure end-to-end latency. Includes process start-up cost, so it is suited to
# sanity checks rather than precise performance comparisons.
#
# Usage: scripts/cross-engine-bench-cli.sh [--iterations N] [--tiers small,medium,large]
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FIXTURES_DIR="${REPO_ROOT}/fixtures/benchmarks"
ITERATIONS=10
TIERS="small,medium,large"

# ─── Argument parsing ─────────────────────────────────────────────────────────

while [[ $# -gt 0 ]]; do
  case "$1" in
    --iterations) ITERATIONS="$2"; shift 2 ;;
    --tiers)      TIERS="$2"; shift 2 ;;
    --help|-h)
      echo "Usage: $0 [--iterations N] [--tiers small,medium,large]"
      exit 0
      ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

# ─── Check that the fixtures exist ────────────────────────────────────────────

if [[ ! -d "$FIXTURES_DIR" ]] || [[ -z "$(ls "$FIXTURES_DIR"/*_config.json 2>/dev/null)" ]]; then
  echo "[fixtures] 生成 benchmark fixtures..."
  python3 "${REPO_ROOT}/scripts/bench-generate-fixtures.py"
fi

# ─── Build the engines ────────────────────────────────────────────────────────

echo "[build] 编译 Go 引擎..."
mkdir -p "${REPO_ROOT}/bin"
(cd "${REPO_ROOT}/pine-go" && go build -o "${REPO_ROOT}/bin/pineapple-run" ./cmd/pineapple-run/)

echo "[build] 编译 Java 引擎..."
(cd "${REPO_ROOT}/pine-java" && mvn package -q -DskipTests -Dmaven.javadoc.skip=true)
echo

# ─── Resolve the Java classpath ───────────────────────────────────────────────

JAVA_JAR="${REPO_ROOT}/pine-java/target/pine-0.7.0.jar"
JAVA_DEPS=$(cd "${REPO_ROOT}/pine-java" && mvn dependency:build-classpath -q -DincludeScope=runtime -Dmdep.outputFile=/dev/stdout 2>/dev/null || echo "")
if [[ -n "$JAVA_DEPS" ]]; then
  JAVA_CP="${JAVA_JAR}:${JAVA_DEPS}"
else
  JAVA_CP="${JAVA_JAR}"
fi

# ─── Timing helper ────────────────────────────────────────────────────────────

time_ms() {
  # Print the current time in milliseconds
  python3 -c "import time; print(int(time.perf_counter() * 1000))"
}

# ─── Single-run helpers ───────────────────────────────────────────────────────

run_go() {
  local config="$1" request="$2"
  "${REPO_ROOT}/bin/pineapple-run" -config "$config" -request "$request" > /dev/null 2>&1
}

run_java() {
  local config="$1" request="$2"
  java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$config" -request "$request" > /dev/null 2>&1
}

# ─── Run the benchmark ────────────────────────────────────────────────────────

echo "╔══════════════════════════════════════════════════════════════╗"
echo "║  Pineapple Cross-Engine CLI Benchmark                       ║"
echo "║  iterations=$ITERATIONS, tiers=$TIERS                       ║"
echo "╚══════════════════════════════════════════════════════════════╝"
echo

printf "| %-20s | %-12s | %-12s |\n" "Fixture" "Go (ms)" "Java (ms)"
printf "|%s|%s|%s|\n" "$(printf '%.0s-' {1..22})" "$(printf '%.0s-' {1..14})" "$(printf '%.0s-' {1..14})"

IFS=',' read -ra TIER_LIST <<< "$TIERS"

for config_file in "${FIXTURES_DIR}"/*_config.json; do
  fixture_name="$(basename "$config_file" _config.json)"

  # Filter by tier
  tier="${fixture_name%%_*}"
  skip=true
  for t in "${TIER_LIST[@]}"; do
    if [[ "$tier" == "$t" ]]; then
      skip=false
      break
    fi
  done
  [[ "$skip" == "true" ]] && continue

  request_file="${FIXTURES_DIR}/${fixture_name}_request.json"
  [[ ! -f "$request_file" ]] && continue

  # Go benchmark
  go_total=0
  for ((i=0; i<ITERATIONS; i++)); do
    start=$(time_ms)
    run_go "$config_file" "$request_file" || true
    end=$(time_ms)
    go_total=$((go_total + end - start))
  done
  go_avg=$((go_total / ITERATIONS))

  # Java benchmark
  java_total=0
  for ((i=0; i<ITERATIONS; i++)); do
    start=$(time_ms)
    run_java "$config_file" "$request_file" || true
    end=$(time_ms)
    java_total=$((java_total + end - start))
  done
  java_avg=$((java_total / ITERATIONS))

  printf "| %-20s | %10d   | %10d   |\n" "$fixture_name" "$go_avg" "$java_avg"
done

echo
echo "注意: CLI benchmark 包含进程启动开销，使用 cross-engine-bench.py 获得更精确的结果。"
