#!/usr/bin/env bash
# Shared environment for cross-validate section scripts.
# Source this file at the top of each section script.
#
# Provides: REPO_ROOT, WORK_DIR, JAVA_CP, TOTAL_SECTIONS
# Functions: fail, pass, normalize_json, java_run, srv_ready
#
# Idempotent: safe to source multiple times.

# Guard against double-source
[[ -n "${_CV_ENV_LOADED:-}" ]] && return 0
_CV_ENV_LOADED=1

set -euo pipefail

_CV_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
: "${TOTAL_SECTIONS:=11}"

# If REPO_ROOT not set, we're running standalone
if [[ -z "${REPO_ROOT:-}" ]]; then
  export REPO_ROOT="$(cd "$_CV_DIR/../.." && pwd)"
fi

# If WORK_DIR not set, create a temp one (standalone mode)
if [[ -z "${WORK_DIR:-}" ]]; then
  export WORK_DIR=$(mktemp -d)
  trap 'rm -rf "$WORK_DIR"' EXIT
fi

# If JAVA_CP not set, run prebuild (standalone mode)
if [[ -z "${JAVA_CP:-}" ]]; then
  source "$_CV_DIR/_prebuild.sh"
fi

# --- Shared state (set defaults only if not already set) ---
: "${_CV_FAIL:=0}"
: "${_CV_SUMMARY:=}"

fail() {
  _CV_FAIL=1
  _CV_SUMMARY+="FAIL: $1\n"
  echo "  ✗ $1" >&2
}

pass() {
  _CV_SUMMARY+="PASS: $1\n"
  echo "  ✓ $1"
}

normalize_json() {
  python3 -c "
import json, sys
def normalize(obj):
    if isinstance(obj, dict):
        return {k: normalize(v) for k, v in obj.items()}
    elif isinstance(obj, list):
        return [normalize(v) for v in obj]
    elif isinstance(obj, (int, float)):
        return float(obj)
    return obj
print(json.dumps(normalize(json.load(sys.stdin)), sort_keys=True))
"
}

java_run() {
  java -cp "$JAVA_CP" "$@"
}

srv_ready() {
  local port=$1 max_wait=10 elapsed=0
  while ! curl -s "http://localhost:$port/health" >/dev/null 2>&1; do
    sleep 0.2
    elapsed=$((elapsed + 1))
    if [[ $elapsed -ge $((max_wait * 5)) ]]; then
      return 1
    fi
  done
  return 0
}
