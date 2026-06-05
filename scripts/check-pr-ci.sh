#!/bin/bash
# check-pr-ci.sh — block until the current branch's PR finishes CI, then report.
#
# Exit codes:
#   0  all CI checks passed AND no new review activity since the last push
#      AND no unresolved review threads — terminal state.
#   1  one or more CI checks failed (all failing jobs listed on stderr).
#   2  no PR found for the current branch, or gh unavailable (treated as
#      non-fatal by the caller hook, but returned distinctly here).
#   75 CI passed, but there is new review activity since the last push or
#      lingering unresolved review threads. The instruction block above
#      this exit explains exactly what to do next. Surfacing this as a
#      non-zero distinct code lets the caller hook fail the outer push so
#      tooling that only inspects exit status (e.g. agent loops) doesn't
#      treat the stderr instructions as silent context.
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

# Lock the "new activity" cut-off NOW, before we spend minutes watching CI —
# any GitHub comment with createdAt > this moment is strictly newer than our
# most recent push. Done up front (not after the CI watch) so legitimate
# review activity arriving during CI is still counted as new. UTC ('Z') keeps
# the jq lexical comparison aligned with GitHub's UTC createdAt fields; a
# local-tz offset like "+08:00" would collate wrong against 'Z' strings.
push_cutoff="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

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
# loop (fetch review comments → understand each one → fix the real issues →
# push again) using concrete gh commands, so no human roundtrip is needed for
# the "address review feedback" step.
#
# Trigger model (intentionally signal-only, not semantic):
#   We do NOT try to grep review-bot output for "APPROVE" strings. The bot is
#   itself an LLM; its wording is non-deterministic, and even an APPROVE often
#   ships with non-blocking suggestions that we still want to evaluate. So the
#   script's only job is to detect whether *anything new* has happened since
#   the current HEAD was pushed, and hand the actual judgment (real issue vs.
#   false positive, blocking vs. nit) over to Claude.
#
# We emit the next-step block when ANY of these hold:
#   - new top-level issue comments since HEAD push time
#   - new inline review (PR review) comments since HEAD push time
#   - new review submissions since HEAD push time (APPROVE / CHANGES_REQUESTED
#     events themselves count as new activity to inspect)
#   - any unresolved review threads (regardless of age — an old unresolved
#     thread still represents debt the loop has not closed)
#
# When none of the above hold, the script exits silently with a single success
# line, so an automated loop reaches a stable terminal state.

owner="$(gh repo view --json owner --jq '.owner.login' 2>/dev/null)"
repo="$(gh repo view --json name --jq '.name' 2>/dev/null)"

# Single GraphQL round-trip pulling everything we need to detect "new activity
# since our push". Each list is independently filtered by createdAt >
# $push_cutoff (locked at script start, before the CI watch) in jq below.
activity_json="$(gh api graphql -f query='
  query($owner:String!, $repo:String!, $pr:Int!) {
    repository(owner:$owner, name:$repo) {
      pullRequest(number:$pr) {
        comments(last:50)        { nodes { createdAt } }
        reviews(last:50)         { nodes { createdAt state } }
        reviewThreads(first:100) {
          nodes {
            isResolved
            comments(last:50) { nodes { createdAt } }
          }
        }
      }
    }
  }' \
  -F owner="$owner" -F repo="$repo" -F pr="$pr_number" 2>/dev/null)"

if [ -z "$activity_json" ]; then
  log "  ! could not query PR activity — assuming work remains, see comments manually."
  new_issue_comments=0
  new_review_submissions=0
  new_inline_comments=0
  unresolved_threads=0
else
  new_issue_comments="$(printf '%s' "$activity_json" \
    | jq --arg t "$push_cutoff" \
        '[.data.repository.pullRequest.comments.nodes[] | select(.createdAt > $t)] | length')"
  new_review_submissions="$(printf '%s' "$activity_json" \
    | jq --arg t "$push_cutoff" \
        '[.data.repository.pullRequest.reviews.nodes[] | select(.createdAt > $t)] | length')"
  new_inline_comments="$(printf '%s' "$activity_json" \
    | jq --arg t "$push_cutoff" \
        '[.data.repository.pullRequest.reviewThreads.nodes[].comments.nodes[] | select(.createdAt > $t)] | length')"
  unresolved_threads="$(printf '%s' "$activity_json" \
    | jq '[.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved==false)] | length')"
fi

total_new=$(( new_issue_comments + new_review_submissions + new_inline_comments ))

# Terminal state: nothing new since the last push AND no lingering unresolved
# threads. Stay silent — an automated loop reads this as "done".
if [ "$total_new" -eq 0 ] && [ "$unresolved_threads" -eq 0 ]; then
  log "  ✓ no new review activity since last push, no unresolved threads — done."
  exit 0
fi

log ""
log "════════════════════════════════════════════════════════════"
log "  ▶ Next step for Claude Code (autonomous loop)"
log "════════════════════════════════════════════════════════════"
log ""
log "PR #${pr_number} has activity since our push cut-off"
log "  ${push_cutoff}"
log "  • new top-level comments:   ${new_issue_comments}"
log "  • new review submissions:   ${new_review_submissions}  (each may be APPROVE / REQUEST_CHANGES / COMMENT)"
log "  • new inline thread replies: ${new_inline_comments}"
log "  • unresolved review threads: ${unresolved_threads}  (any age)"
log ""
log "An APPROVE verdict from the review bot does NOT mean there is nothing to"
log "do — bots are LLMs, their wording is non-deterministic, and they often"
log "approve while still flagging real issues as non-blocking. Read every new"
log "comment and every unresolved thread, then decide on the merits."
log ""
log "Step 1. Fetch the current review surface:"
log ""
log "    # Top-level issue-style comments (often where the review bot posts)"
log "    gh pr view ${pr_number} --json comments \\"
log "        --jq '.comments[] | {author: .author.login, createdAt, body}'"
log ""
log "    # Formal Review submissions (APPROVE / REQUEST_CHANGES / COMMENT)"
log "    gh api graphql -f query='"
log "      query(\$owner:String!, \$repo:String!, \$pr:Int!) {"
log "        repository(owner:\$owner, name:\$repo) {"
log "          pullRequest(number:\$pr) {"
log "            reviews(last:20) { nodes { author { login } state body submittedAt } }"
log "          }"
log "        }"
log "      }' \\"
log "      -F owner=\"${owner}\" -F repo=\"${repo}\" -F pr=${pr_number}"
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
log "      -F owner=\"${owner}\" -F repo=\"${repo}\" -F pr=${pr_number}"
log ""
log "Step 2. For each item, judge on the merits — do NOT just chase APPROVE:"
log "  - Open the cited file:line and read the surrounding code yourself."
log "  - Decide: is the concern real? (bots produce false positives — that's"
log "    fine, but the decision must be backed by reading the code, not by"
log "    trusting the verdict label)"
log "  - If real: it's a work item, whether the bot called it blocking or not."
log "    We do not ship technical debt just because a reviewer waved it through."
log "  - If a false positive: leave it; do not fabricate a fix to placate."
log ""
log "Step 3. Fix every real item:"
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
log "  block will reappear if any further activity appears after the new HEAD."
log ""
log "Termination: when this script next prints"
log "    ✓ no new review activity since last push, no unresolved threads — done."
log "the loop is done. Stop pushing and tell the user the PR is ready."
log ""
log "Current state: push_cutoff=${push_cutoff}, new_total=${total_new}, unresolved_threads=${unresolved_threads}, reviewDecision=${review_decision}"
log "════════════════════════════════════════════════════════════"
exit 75
