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
# crawling" turns into "kill and retry" rather than "burn the whole
# budget and die".
#
# Retrying alone does not change which mirror is served, though. The runner
# points sources.list at mirror+file:/etc/apt/apt-mirrors.txt, and per
# apt-transport-mirror(1) two things hold: the method only fails over to
# another mirror when a fetch *fails*, and mirrors are tried in ascending
# `priority:` order. #164's failure mode was a mirror that answered and
# then crawled at ~26 KB/s — never a failure, so failover never fired, and
# every retry went back to the same priority:1 host. So each retry now also
# rotates the priority order, which is what actually moves the next attempt
# to a different mirror.
#
# Usage:
#   scripts/ci-apt-install.sh <package>...
#
# Tunables (env):
#   ATTEMPTS        max attempts per phase        (default 3)
#   ATTEMPT_TIMEOUT seconds per attempt           (default 300)
#   APT_MIRRORLIST  mirrorlist path               (default /etc/apt/apt-mirrors.txt)
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

APT_MIRRORLIST="${APT_MIRRORLIST:-/etc/apt/apt-mirrors.txt}"

# Rotate the mirrorlist left by one entry and renumber priorities from 1, so
# the next attempt starts at a different host than this one. Rewriting the
# priorities rather than just reordering lines is required: the order in the
# file is not what apt honours, `priority:` is, and a mirror with no explicit
# priority sorts last.
#
# The shift is always 1 relative to the current file, so repeated calls walk
# the list one host at a time. Rotating by the attempt number instead would
# make attempt 3 land back on the original head for a 3-mirror list.
#
# Comments and blank lines are preserved verbatim. Writes need sudo (the file
# is root-owned), so readability is what gets checked here, not writability.
#
# Parsing is tab-field aware because the format is: URI, then a TAB, then
# metadata separated by tabs or spaces. Rewriting the line as a single string
# would join URI and metadata with a space and apt would then read the space
# as part of the URI. Only the priority token is dropped; any other metadata
# (arch:, codename:, component:, ...) is carried through, since dropping it
# would silently widen which files a partial mirror is asked to serve.
rotate_mirrorlist() {
  local tmp
  [[ -r "$APT_MIRRORLIST" ]] || return 1
  tmp=$(mktemp) || return 1
  awk -F'\t' -v s=1 '
    /^[[:space:]]*(#|$)/ { print; next }
    { uri[++n] = $1
      meta[n] = ""
      for (f = 2; f <= NF; f++) {
        # Split on either separator the format allows, then keep everything
        # except the priority we are about to reassign.
        c = split($f, kv, /[ \t]+/)
        for (k = 1; k <= c; k++)
          if (kv[k] != "" && kv[k] !~ /^priority:[0-9]+$/)
            meta[n] = meta[n] "\t" kv[k]
      }
    }
    END {
      for (i = 1; i <= n; i++) {
        j = ((i - 1 + s) % n) + 1
        printf "%s\tpriority:%d%s\n", uri[j], i, meta[j]
      }
    }' "$APT_MIRRORLIST" > "$tmp" || { rm -f "$tmp"; return 1; }
  # Non-empty guard: an awk failure that still exits 0 would otherwise leave
  # the runner with no mirrors at all, turning a slow day into a hard failure.
  [[ -s "$tmp" ]] || { rm -f "$tmp"; return 1; }
  sudo cp "$tmp" "$APT_MIRRORLIST"
  local rc=$?
  rm -f "$tmp"
  # Report the new first mirror, skipping comments so the log names a host
  # rather than whatever header the image happens to put at the top.
  [[ $rc -eq 0 ]] && echo "    next attempt starts at: $(grep -v '^[[:space:]]*\(#\|$\)' "$APT_MIRRORLIST" | head -1 | cut -f1)"
  return $rc
}

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
      # Move to a different mirror for the next attempt. Best-effort: a
      # missing or read-only mirrorlist (any non-runner environment) just
      # means the retry behaves as it did before, so do not fail on it.
      rotate_mirrorlist \
        || echo "    (mirrorlist not rotatable, retrying same mirror order)" >&2
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
