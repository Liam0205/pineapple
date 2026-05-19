#!/usr/bin/env bash
# Cross-validation entry point: run Go vs Java vs Python parity checks.
#
# Usage:
#   bash scripts/cross-validate.sh          # Run all sections
#   bash scripts/cross-validate.sh 1-5      # Run sections 1 through 5
#   bash scripts/cross-validate.sh 1,3,8    # Run sections 1, 3, and 8
#   bash scripts/cross-validate.sh 1-5,8,10-11  # Mixed ranges
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
CV_DIR="$SCRIPT_DIR/cross-validate"

export REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
export WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

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

TOTAL_SECTIONS=11
export TOTAL_SECTIONS

if [[ $# -gt 0 ]]; then
  SECTIONS=($(parse_sections "$1"))
else
  SECTIONS=($(seq 1 $TOTAL_SECTIONS))
fi

# --- Pre-build ---
source "$CV_DIR/_prebuild.sh"

# --- Shared state ---
export _CV_FAIL=0
export _CV_SUMMARY=""

# --- Run selected sections ---
for sec in "${SECTIONS[@]}"; do
  sec_file=$(printf "%s/%02d-*.sh" "$CV_DIR" "$sec")
  # Glob to find the file
  matched=( $sec_file )
  if [[ ! -f "${matched[0]}" ]]; then
    echo "WARNING: section $sec not found (no file matching $sec_file)" >&2
    continue
  fi
  source "${matched[0]}"
done

# --- Summary ---
echo
echo "╔══════════════════════════════════════╗"
echo "║   Cross-Validation Summary           ║"
echo "╚══════════════════════════════════════╝"
echo -e "$_CV_SUMMARY"

exit $_CV_FAIL
