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

# Normalize to base 10 before either value reaches arithmetic. A caller passing
# a zero-padded number (ATTEMPTS=09) would otherwise abort `[[ -lt ]]` with
# "value too great for base", which under `set -u` skips the retry body and
# silently turns off the mirror rotation below rather than failing loudly.
if [[ "$ATTEMPTS" == +([0-9]) ]]; then ATTEMPTS=$(( 10#$ATTEMPTS )); fi
if [[ "$ATTEMPT_TIMEOUT" == +([0-9]) ]]; then ATTEMPT_TIMEOUT=$(( 10#$ATTEMPT_TIMEOUT )); fi

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
# The rotation is computed over apt's own ordering, not the file's line order.
# Those two differ: a list written as `A priority:3 / B priority:1` is tried
# B-first, so shifting by line order would move A to the head and leave apt
# still preferring B — no rotation at all, in exactly the case this function
# exists for. Mirrors are therefore sorted by effective priority first (missing
# priority sorts last, matching apt), and the shift applies to that sequence.
#
# Comment and blank lines keep their content but not their position: they are
# emitted as encountered while mirror lines are held back to END, so they end
# up above the mirror block. apt ignores both, so this is harmless.
#
# Writes need sudo (the file is root-owned), so readability is what gets
# checked here, not writability.
#
# Parsing is tab-field aware because the format is: URI, then a TAB, then
# metadata separated by tabs or spaces. Rewriting the line as a single string
# would join URI and metadata with a space and apt would then read the space
# as part of the URI. Metadata other than priority (arch:, codename:,
# component:, ...) is carried through — content preserved, though separators
# are normalized to tabs — since dropping it would silently widen which files
# a partial mirror is asked to serve.
rotate_mirrorlist() {
  local tmp
  [[ -r "$APT_MIRRORLIST" ]] || return 1
  tmp=$(mktemp) || return 1
  awk -F'\t' -v s=1 '
    /^[[:space:]]*(#|$)/ { print; next }
    { uri[++n] = $1
      meta[n] = ""
      # Sentinel so mirrors with no explicit priority sort last, as apt does.
      prio[n] = 2147483647
      for (f = 2; f <= NF; f++) {
        # Split on either separator the format allows, then keep everything
        # except the priority, which we record and then reassign.
        c = split($f, kv, /[ \t]+/)
        for (k = 1; k <= c; k++) {
          if (kv[k] == "") continue
          if (kv[k] ~ /^priority:[0-9]+$/) {
            sub(/^priority:/, "", kv[k])
            prio[n] = kv[k] + 0
          } else {
            meta[n] = meta[n] "\t" kv[k]
          }
        }
      }
    }
    END {
      if (n == 0) exit 1
      # Insertion sort by effective priority into ord[], stable so that equal
      # priorities keep file order (apt picks among equals at random anyway).
      for (i = 1; i <= n; i++) {
        for (j = i; j > 1 && prio[ord[j - 1]] > prio[i]; j--)
          ord[j] = ord[j - 1]
        ord[j] = i
      }
      for (i = 1; i <= n; i++) {
        j = ord[((i - 1 + s) % n) + 1]
        printf "%s\tpriority:%d%s\n", uri[j], i, meta[j]
      }
    }' "$APT_MIRRORLIST" > "$tmp" || { rm -f "$tmp"; return 1; }
  # Belt and braces. awk already exits 1 when it read no mirror lines, so a
  # comments-only list is rejected above and this check is currently redundant.
  # It is kept as a cheap invariant on the thing that actually matters — never
  # install a list with no mirrors — so that a future change to the awk cannot
  # reintroduce that outcome silently. Note `-s` alone would not do: a file of
  # comments is non-empty yet has no mirrors.
  grep -Eqv '^[[:space:]]*(#|$)' "$tmp" || { rm -f "$tmp"; return 1; }
  # Replace atomically. `cp` onto the live file truncates first, so a failure
  # mid-write would leave apt with a half-written list; rename cannot. Keep a
  # one-time backup of the image's original list for post-mortems.
  # Test-then-copy rather than `cp -n`: coreutils 9.4 warns that -n's behaviour
  # is non-portable and may change, and "skip if the target exists" is exactly
  # the behaviour being relied on here.
  [[ -e "$APT_MIRRORLIST.orig" ]] \
    || sudo cp "$APT_MIRRORLIST" "$APT_MIRRORLIST.orig" 2>/dev/null || true
  local rc=0
  # Carry the current mode across the replacement. `mv` moves the temp file's
  # inode, so without this the list inherits mktemp's 0600 and — being
  # root-owned on a runner — stops being readable by this non-root script,
  # which would make the very next rotation bail out at the `-r` test above.
  # Plain `cp` onto the live file used to preserve mode implicitly; the atomic
  # form has to do it explicitly.
  local mode
  mode=$(stat -c %a "$APT_MIRRORLIST" 2>/dev/null) || mode=644
  sudo install -m "$mode" "$tmp" "$APT_MIRRORLIST.new" \
    && sudo mv "$APT_MIRRORLIST.new" "$APT_MIRRORLIST" || rc=$?
  rm -f "$tmp"
  # Do not leave a stray .new beside the live list if either step failed; it
  # would be one more thing to explain during a post-mortem.
  [[ $rc -eq 0 ]] || sudo rm -f "$APT_MIRRORLIST.new" 2>/dev/null || true
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
