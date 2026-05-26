#!/usr/bin/env bash
# Cross-validation entry point: run Go vs Java vs Python parity checks.
#
# Usage:
#   bash scripts/cross-validate.sh              # Run all sections (parallel)
#   bash scripts/cross-validate.sh 1-5          # Run sections 1 through 5
#   bash scripts/cross-validate.sh 1,3,8        # Run sections 1, 3, and 8
#   bash scripts/cross-validate.sh 1-5,8,10-11  # Mixed ranges
#   bash scripts/cross-validate.sh --serial 1-5 # Force serial execution
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CV_DIR="$SCRIPT_DIR/cross-validate"

export REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
export WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

# --- Options ---
PARALLEL=1
if [[ "${1:-}" == "--serial" ]]; then
  PARALLEL=0
  shift
fi

# --- Argument parsing: expand "1-5,8,10-11" into a list of section numbers ---
parse_sections() {
  local input="$1"
  local result=()

  IFS=',' read -ra parts <<< "$input"
  for part in "${parts[@]}"; do
    if [[ "$part" == *-* ]]; then
      local start="${part%-*}"
      local end="${part#*-}"
      for ((n=start; n<=end; n++)); do
        result+=("$n")
      done
    else
      result+=("$part")
    fi
  done
  echo "${result[@]}"
}

# Total number of cross-validate sections — derived from the on-disk
# section script count, not a hard-coded constant. Avoids the recurring
# "bump TOTAL_SECTIONS in two files" anti-pattern flagged across multiple
# llmdoc reflections (p2-refactor-cross-validate-scripts, v072-074-llmdoc).
TOTAL_SECTIONS=$(ls "$CV_DIR"/[0-9][0-9]-*.sh 2>/dev/null | wc -l)
export TOTAL_SECTIONS

if [[ $# -gt 0 ]]; then
  SECTIONS=($(parse_sections "$1"))
else
  SECTIONS=($(seq 1 $TOTAL_SECTIONS))
fi

# --- Pre-build (always serial — shared binaries) ---
source "$CV_DIR/_prebuild.sh"

# --- Shared state ---
export _CV_FAIL=0
export _CV_SUMMARY=""

# --- Execution ---
if [[ $PARALLEL -eq 1 && ${#SECTIONS[@]} -gt 1 ]]; then
  # Parallel mode: each section runs in a subshell, output captured to file
  RESULT_DIR="$WORK_DIR/results"
  mkdir -p "$RESULT_DIR"

  pids=()
  for sec in "${SECTIONS[@]}"; do
    sec_file=$(printf "%s/%02d-*.sh" "$CV_DIR" "$sec")
    matched=( $sec_file )
    if [[ ! -f "${matched[0]}" ]]; then
      echo "WARNING: section $sec not found (no file matching $sec_file)" >&2
      continue
    fi
    (
      export _CV_FAIL=0
      export _CV_SUMMARY=""
      source "${matched[0]}"
      # Write results
      echo "$_CV_FAIL" > "$RESULT_DIR/${sec}.rc"
      echo -e "$_CV_SUMMARY" > "$RESULT_DIR/${sec}.summary"
    ) > "$RESULT_DIR/${sec}.out" 2>&1 &
    pids+=("$!:$sec")
  done

  # Wait for all and collect results
  any_fail=0
  for entry in "${pids[@]}"; do
    pid="${entry%%:*}"
    sec="${entry##*:}"
    if ! wait "$pid"; then
      any_fail=1
    fi
  done

  # Print output in order
  for sec in "${SECTIONS[@]}"; do
    if [[ -f "$RESULT_DIR/${sec}.out" ]]; then
      cat "$RESULT_DIR/${sec}.out"
    fi
    if [[ -f "$RESULT_DIR/${sec}.rc" ]]; then
      rc=$(cat "$RESULT_DIR/${sec}.rc")
      if [[ "$rc" != "0" ]]; then
        _CV_FAIL=1
      fi
    fi
    if [[ -f "$RESULT_DIR/${sec}.summary" ]]; then
      _CV_SUMMARY+="$(cat "$RESULT_DIR/${sec}.summary")\n"
    fi
  done
else
  # Serial mode
  for sec in "${SECTIONS[@]}"; do
    sec_file=$(printf "%s/%02d-*.sh" "$CV_DIR" "$sec")
    matched=( $sec_file )
    if [[ ! -f "${matched[0]}" ]]; then
      echo "WARNING: section $sec not found (no file matching $sec_file)" >&2
      continue
    fi
    source "${matched[0]}"
  done
fi

# --- Summary ---
echo
echo "╔══════════════════════════════════════╗"
echo "║   Cross-Validation Summary           ║"
echo "╚══════════════════════════════════════╝"
echo -e "$_CV_SUMMARY"

exit $_CV_FAIL
