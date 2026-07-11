#!/usr/bin/env bash
# ci-apt-install.sh — resilient apt-get update + install for CI runners.
#
# Why this exists: a single slow Azure archive mirror day can stall a
# plain `timeout 600 sudo apt-get install ...` past its whole budget and
# fail the workflow before any real work starts. This happened twice:
#   #125 (2026-06-18) — fixed by bumping timeout 300s -> 600s
#   #164 (2026-07-10) — 600s breached again: the mirror served cmake
#                       (11.2 MB) at ~26 KB/s, 433s for that one package
# A bigger static timeout just moves the cliff. This wrapper retries
# instead: each attempt gets a hard per-attempt timeout so "mirror is
# crawling" turns into "kill and retry" (mirror rotation usually lands a
# healthy endpoint) rather than "burn the whole budget and die".
#
# Usage:
#   scripts/ci-apt-install.sh <package>...
#
# Tunables (env):
#   ATTEMPTS        max attempts per phase        (default 3)
#   ATTEMPT_TIMEOUT seconds per attempt           (default 300)
#
# Note for callers: keep the package list lean. The GitHub runner image
# preinstalls cmake (newer than apt's, and earlier in PATH), g++-13 and
# build-essential — installing them via apt is dead weight that only
# widens the slow-mirror exposure window (#164's 15.7 MB vs the ~1.5 MB
# actually needed). Assert preinstalled tools with `cmake --version`
# after this script instead of apt-installing shadowed duplicates.

set -u

ATTEMPTS="${ATTEMPTS:-3}"
ATTEMPT_TIMEOUT="${ATTEMPT_TIMEOUT:-300}"

if [[ $# -eq 0 ]]; then
  echo "usage: $0 <package>..." >&2
  exit 2
fi

retry() {
  local desc="$1"
  shift
  local attempt rc
  for attempt in $(seq 1 "$ATTEMPTS"); do
    echo "==> ${desc} (attempt ${attempt}/${ATTEMPTS}, timeout ${ATTEMPT_TIMEOUT}s)"
    timeout "$ATTEMPT_TIMEOUT" "$@"
    rc=$?
    [[ $rc -eq 0 ]] && return 0
    echo "    attempt ${attempt} failed (rc=${rc})" >&2
    if [[ "$attempt" -lt "$ATTEMPTS" ]]; then
      # A kill mid-unpack can leave dpkg half-configured; repair before
      # retrying (no-op in the common kill-mid-download case).
      sudo dpkg --configure -a >/dev/null 2>&1 || true
      sleep $((attempt * 10))
    fi
  done
  echo "ERROR: ${desc} failed after ${ATTEMPTS} attempts" >&2
  return 1
}

# Acquire::Retries covers transient connection drops within one attempt;
# the outer loop covers the "mirror up but crawling" case it does not.
# DPkg::Lock::Timeout waits out unattended-upgrades style lock holders
# instead of failing the attempt instantly.
APT_OPTS=(-o Acquire::Retries=3 -o DPkg::Lock::Timeout=60)

retry "apt-get update" \
  sudo apt-get update "${APT_OPTS[@]}" || exit 1
retry "apt-get install $*" \
  sudo apt-get install -y --no-install-recommends "${APT_OPTS[@]}" "$@" || exit 1
