#!/usr/bin/env bash
# Open (or update) a GitHub issue for a failed nightly sanitizer stress run.
#
# Usage: file-sanitizer-issue.sh <kind> <log-file>
#   kind     : "tsan" or "asan" — drives title, label, and report extraction.
#   log-file : the tee'd sanitizer stress output to mine for the report.
#
# Why a script instead of inline YAML: both the tsan and asan jobs need the
# identical label-safety + report-extraction + dedup logic. Inlining it twice
# in the workflow is how the diff-fuzz workflow ended up with two subtly
# different copies. One source of truth here.
#
# Requires GH_TOKEN in the environment (set by the workflow step).
set -euo pipefail

KIND="${1:?usage: file-sanitizer-issue.sh <tsan|asan> <log-file>}"
LOG="${2:?usage: file-sanitizer-issue.sh <tsan|asan> <log-file>}"

REPO="${GITHUB_REPOSITORY:-Liam0205/pineapple}"
RUN_URL="${GITHUB_SERVER_URL:-https://github.com}/${REPO}/actions/runs/${GITHUB_RUN_ID:-0}"
DATE_UTC="$(date -u +%Y-%m-%d)"

case "$KIND" in
    tsan)
        TITLE_PREFIX="Nightly sanitizer: ThreadSanitizer race"
        # data-race is the precise label; ensure it exists below.
        LABELS="bug,infra,data-race"
        # Extract from the first TSan WARNING through the SUMMARY line.
        REPORT="$(awk '/WARNING: ThreadSanitizer/{f=1} f{print} /SUMMARY: ThreadSanitizer/{exit}' "$LOG" 2>/dev/null || true)"
        ;;
    asan)
        TITLE_PREFIX="Nightly sanitizer: ASan/UBSan error"
        LABELS="bug,infra"
        # ASan and UBSan have different headers; capture either, ~80 lines.
        REPORT="$(awk '/ERROR: AddressSanitizer|runtime error:|SUMMARY: (AddressSanitizer|UndefinedBehaviorSanitizer)/{f=1} f{print; n++} n>80{exit}' "$LOG" 2>/dev/null || true)"
        ;;
    *)
        echo "unknown kind: $KIND" >&2
        exit 2
        ;;
esac

# Fallback: if the structured extraction found nothing (sanitizer aborted in a
# shape our awk didn't match, or the failure was a build error), include the
# log tail so the issue still carries a signal.
if [ -z "$REPORT" ]; then
    REPORT="$(tail -n 80 "$LOG" 2>/dev/null || echo '(no log captured)')"
    REPORT_NOTE="_No structured sanitizer report matched; showing log tail._"
else
    REPORT_NOTE="_Sanitizer report (extracted from the stress log)._"
fi

# Label safety: createIssue fails outright if any --label doesn't exist
# (this, plus a missing permissions block, is what silently broke earlier
# auto-issue attempts). Create each label idempotently before use. --force
# updates an existing label in place rather than erroring.
IFS=',' read -ra LABEL_ARR <<< "$LABELS"
for lbl in "${LABEL_ARR[@]}"; do
    case "$lbl" in
        data-race) gh label create "$lbl" --repo "$REPO" --color B60205 \
            --description "Concurrency data race (TSan)" --force >/dev/null 2>&1 || true ;;
        *) # bug/infra already exist; --force keeps this a no-op-ish refresh.
           gh label create "$lbl" --repo "$REPO" --force >/dev/null 2>&1 || true ;;
    esac
done

# Dedup: if an open issue with the same title prefix already exists, comment on
# it instead of opening a duplicate every night. Match by title prefix + label.
EXISTING="$(gh issue list --repo "$REPO" --state open --label "infra" \
    --search "$TITLE_PREFIX in:title" --json number --jq '.[0].number' 2>/dev/null || true)"

BODY="$(cat <<EOF
## ${TITLE_PREFIX}

| | |
|---|---|
| **Run** | ${RUN_URL} |
| **Date** | ${DATE_UTC} |
| **Kind** | ${KIND} |

This is from the nightly sanitizer stress workflow (STRESS_MULTIPLIER-amplified
re-run of the PR-time ${KIND} smoke), built to surface the narrow,
runner-specific concurrency bug that single-pass CI keeps missing.

${REPORT_NOTE}

<details>
<summary>Sanitizer report</summary>

\`\`\`
${REPORT}
\`\`\`
</details>

Full stress log: see the run's step log at ${RUN_URL}.
EOF
)"

if [ -n "$EXISTING" ]; then
    echo "Commenting on existing issue #${EXISTING}"
    gh issue comment "$EXISTING" --repo "$REPO" --body "$BODY"
else
    echo "Opening new issue"
    gh issue create --repo "$REPO" \
        --title "${TITLE_PREFIX} (${DATE_UTC})" \
        --label "$LABELS" \
        --body "$BODY"
fi
