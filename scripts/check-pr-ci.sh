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
log "  → PR review decision: ${review_decision}"
log "════════════════════════════════════════════════════════════"

# ── instruction block for Claude Code ──────────────────────────────────────
# When this script runs inside a Claude Code background bash task, the entire
# stderr above is fed back into the main conversation on completion. The block
# below is written for Claude to consume directly: it spells out the next-step
# loop (fetch review comments → fix every one → push again) using concrete gh
# commands, so no human roundtrip is needed for the "address review feedback"
# step. The loop terminates naturally — a future re-push re-enters this script,
# and if the PR review is APPROVED with no unresolved comments, no further
# instructions are emitted.
#
# We only emit the block when there is actual work for Claude to do:
#   - CI is green (we are in the success path below the failure exit)
#   - PR is not yet APPROVED, OR there are unresolved review threads
#
# When the PR is APPROVED with no outstanding threads, the block is suppressed
# so an automated workflow knows it has reached the terminal state.

# Check whether there are any unresolved review threads. The graphql query is
# the only reliable signal — `gh pr view --json comments` returns issue-style
# top-level comments but not review-thread state (resolved vs unresolved).
unresolved_threads="$(gh api graphql -f query='
  query($owner:String!, $repo:String!, $pr:Int!) {
    repository(owner:$owner, name:$repo) {
      pullRequest(number:$pr) {
        reviewThreads(first:100) {
          nodes { isResolved }
        }
      }
    }
  }' \
  -F owner="$(gh repo view --json owner --jq '.owner.login' 2>/dev/null)" \
  -F repo="$(gh repo view --json name --jq '.name' 2>/dev/null)" \
  -F pr="$pr_number" \
  --jq '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved==false)] | length' \
  2>/dev/null)"
[ -z "$unresolved_threads" ] && unresolved_threads=0

# Terminal state: APPROVED and nothing unresolved → nothing for Claude to do.
if [ "$review_decision" = "APPROVED" ] && [ "$unresolved_threads" = "0" ]; then
  log "  ✓ PR is APPROVED with no unresolved threads — nothing more to do."
  exit 0
fi

log ""
log "════════════════════════════════════════════════════════════"
log "  ▶ Next step for Claude Code (autonomous loop)"
log "════════════════════════════════════════════════════════════"
log ""
log "PR #${pr_number} has review feedback to address before this branch can"
log "ship. Work through every comment — review bots and human reviewers alike,"
log "blocking and non-blocking, important and minor. Goal: zero technical debt"
log "left behind."
log ""
log "Step 1. Fetch the current review surface:"
log ""
log "    # Top-level issue-style comments (often where the review bot posts)"
log "    gh pr view ${pr_number} --json comments \\"
log "        --jq '.comments[] | {author: .author.login, createdAt, body}'"
log ""
log "    # Inline review threads with resolved state and per-thread comments"
log "    gh api graphql -f query='"
log "      query(\$owner:String!, \$repo:String!, \$pr:Int!) {"
log "        repository(owner:\$owner, name:\$repo) {"
log "          pullRequest(number:\$pr) {"
log "            reviewThreads(first:100) {"
log "              nodes {"
log "                isResolved"
log "                path"
log "                line"
log "                comments(first:50) { nodes { author { login } body createdAt } }"
log "              }"
log "            }"
log "          }"
log "        }"
log "      }' \\"
log "      -F owner=\"\$(gh repo view --json owner --jq '.owner.login')\" \\"
log "      -F repo=\"\$(gh repo view --json name --jq '.name')\" \\"
log "      -F pr=${pr_number}"
log ""
log "Step 2. Filter to ACTIONABLE feedback only:"
log "  - Skip threads where isResolved == true."
log "  - Skip comments older than the most recent push (HEAD authored at"
log "    \$(git log -1 --format=%cI HEAD)) unless explicitly re-raised in a"
log "    newer comment — they likely refer to already-fixed code."
log "  - Treat both blocking and non-blocking points as work items."
log ""
log "Step 3. Fix every actionable item. For each:"
log "  - Read the cited file:line, understand the concern."
log "  - Apply the smallest correct change; do NOT bundle drive-by refactors."
log "  - Run the local validations the original reviewer would have run"
log "    (lint, unit tests, cross-validate section if the change touches one)."
log "  - Commit per the standard isolation rules (per-language, see"
log "    llmdoc/guides/standard-workflow.md): one logical fix = one commit."
log ""
log "Step 4. Push again:"
log ""
log "    git push"
log ""
log "  The push re-enters this hook chain. CI will be re-watched and this"
log "  block will reappear if any review feedback remains, looping until the"
log "  PR reaches APPROVED with zero unresolved threads."
log ""
log "Termination: when this script next prints"
log "    ✓ PR is APPROVED with no unresolved threads — nothing more to do."
log "the loop is done. Stop pushing and tell the user the PR is ready."
log ""
log "Current state: reviewDecision=${review_decision}, unresolved_threads=${unresolved_threads}"
log "════════════════════════════════════════════════════════════"
exit 0
