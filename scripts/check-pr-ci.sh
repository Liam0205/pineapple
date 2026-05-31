#!/bin/bash
# check-pr-ci.sh — block until the current branch's PR finishes CI, then report.
#
# Exit codes:
#   0  all CI checks passed (a review-status reminder is still printed to stderr)
#   1  one or more CI checks failed (all failing jobs listed on stderr)
#   2  no PR found for the current branch, or gh unavailable (treated as non-fatal
#      by the caller hook, but returned distinctly here)
#
# This script does NOT push. It only inspects the already-pushed branch's PR.
set -uo pipefail

log() { echo "$@" >&2; }

if ! command -v gh >/dev/null 2>&1; then
  log "post-push: gh CLI not found — skipping CI check."
  exit 2
fi

# jq is required to parse the check list. The gh CLI itself ships jq-style
# filtering via --jq, but we also do a standalone jq parse below; without it we
# could silently miss failures (false green), so treat its absence as fatal.
if ! command -v jq >/dev/null 2>&1; then
  log "post-push: jq not found — cannot reliably parse CI checks. Failing safe."
  exit 1
fi

branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"

# Resolve the PR number for this branch. If none, nothing to watch.
pr_number="$(gh pr view --json number --jq '.number' 2>/dev/null)"
if [ -z "$pr_number" ]; then
  log "post-push: no open PR for branch '$branch' yet — skipping CI check."
  log "post-push: (open a PR, then CI status will be watched on the next push)"
  exit 2
fi

log "post-push: watching CI for PR #${pr_number} (branch '$branch')..."

# After a fresh push, GitHub needs a few seconds to register check runs for the
# new head commit. Poll until at least one check appears (or give up), so we
# don't mistake "not created yet" for "no CI". Tunable via env:
#   PINE_PREPUSH_CHECK_POLL_TRIES    (default 24)
#   PINE_PREPUSH_CHECK_POLL_INTERVAL (default 5, seconds)
poll_tries="${PINE_PREPUSH_CHECK_POLL_TRIES:-24}"
poll_interval="${PINE_PREPUSH_CHECK_POLL_INTERVAL:-5}"
for _ in $(seq 1 "$poll_tries"); do
  cnt="$(gh pr checks "$pr_number" --json state --jq 'length' 2>/dev/null)"
  if [ -n "$cnt" ] && [ "$cnt" -gt 0 ]; then
    break
  fi
  sleep "$poll_interval"
done

# Block until all checks finish. --fail-fast returns as soon as one fails.
# We intentionally ignore the watch exit code here and re-derive the final
# verdict from a fresh --json query, so the reporting logic is single-sourced.
gh pr checks "$pr_number" --watch --fail-fast --interval 15 >/dev/null 2>&1 || true

# Fresh snapshot of all checks.
checks_json="$(gh pr checks "$pr_number" --json name,state,bucket,link 2>/dev/null)"
if [ -z "$checks_json" ]; then
  log "post-push: could not read CI checks for PR #${pr_number}."
  exit 2
fi

# Collect every failing job (bucket == fail or cancel) as a list of
# "name<TAB>link" lines. Using jq (required above) keeps this dependency-light
# and avoids a silent empty result when an interpreter is missing.
failures="$(printf '%s' "$checks_json" \
  | jq -r '.[] | select(.bucket=="fail" or .bucket=="cancel") | "\(.name)\t\(.link // "")"')"

review_decision="$(gh pr view "$pr_number" --json reviewDecision --jq '.reviewDecision' 2>/dev/null)"
[ -z "$review_decision" ] && review_decision="(no review yet)"

if [ -n "$failures" ]; then
  log ""
  log "════════════════════════════════════════════════════════════"
  log "  ✗ post-push: CI FAILED for PR #${pr_number}"
  log "  Failing checks:"
  while IFS=$'\t' read -r name link; do
    [ -z "$name" ] && continue
    log "    • ${name}"
    [ -n "$link" ] && log "        ${link}"
  done <<< "$failures"
  log ""
  log "  → Investigate the failing jobs above."
  log "  → Also check the PR review status (currently: ${review_decision})."
  log "════════════════════════════════════════════════════════════"
  exit 1
fi

log ""
log "════════════════════════════════════════════════════════════"
log "  ✓ post-push: all CI checks passed for PR #${pr_number}"
log "  → Now check the PR review status (currently: ${review_decision})."
if [ "$review_decision" != "APPROVED" ]; then
  log "  → PR is not yet APPROVED — review before merging."
fi
log "════════════════════════════════════════════════════════════"
exit 0
