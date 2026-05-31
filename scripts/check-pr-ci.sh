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

branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null)"

# Resolve the PR number for this branch. If none, nothing to watch.
pr_number="$(gh pr view --json number --jq '.number' 2>/dev/null)"
if [ -z "$pr_number" ]; then
  log "post-push: no open PR for branch '$branch' yet — skipping CI check."
  log "post-push: (open a PR, then CI status will be watched on the next push)"
  exit 2
fi

log "post-push: watching CI for PR #${pr_number} (branch '$branch')..."

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

# Collect every failing job (bucket == fail or cancel), as a list of "name<TAB>link".
# Pass JSON via env var so the heredoc keeps stdin free and bash does no escaping.
failures="$(CHECKS_JSON="$checks_json" python3 - <<'PY'
import json, os
data = json.loads(os.environ["CHECKS_JSON"])
bad = [c for c in data if c.get("bucket") in ("fail", "cancel")]
for c in bad:
    print("{}\t{}".format(c.get("name", "?"), c.get("link", "")))
PY
)"

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
