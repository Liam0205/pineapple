# Git hooks

This directory holds the repo's git hooks. Enable them once with:

```bash
git config core.hooksPath .githooks
```

(CI sets `CI` / `GITHUB_ACTIONS`, which the hook detects and skips, so this is
safe to enable locally without affecting automation.)

## `pre-commit`

A **fast, staged-only** lint gate. It checks just the files staged for the
current commit (`git diff --cached`), so unrelated in-tree violations never
block or slow down an isolated commit. Each language uses a file-level tool:

| Files | Tool | Check |
| --- | --- | --- |
| `*.cpp` `*.hpp` `*.cc` `*.h` | `clang-format` | `--dry-run --Werror` |
| `*.go` | `gofmt` | `gofmt -l` (lists unformatted files) |
| `*.py` | `ruff` | `ruff check` |

Any violation aborts the commit and prints the exact `fix:` command to run
(e.g. `clang-format -i <files> && git add <files>`). A tool that isn't
installed is skipped silently, and CI (`CI` / `GITHUB_ACTIONS`) short-circuits
the whole hook. The heavier project-wide linters still run at push time — see
`pre-push` below.

## `pre-push`

Two responsibilities, in order:

1. **Lint gate.** Runs the per-language linters (`ruff`, `golangci-lint`,
   `mvn checkstyle:check`, `clang-format`) for whichever sub-projects are
   present. Any failure aborts the push.

2. **Post-push CI watch (self-wrapping).** git has no native `post-push` hook,
   so this one emulates it. After lint passes it performs the *real* push
   itself (the "inner" push, marked with the `GIT_POSTPUSH` env var so the
   re-entered hook short-circuits), then blocks on the remote PR's CI via
   `scripts/check-pr-ci.sh` and prints a ✓/✗ report.

### ⚠️ The `git push` exit code is NOT trustworthy with this hook enabled

This is the key thing to understand. Because the hook performs the real push
*inside* itself, the **outer** `git push` you typed is guaranteed to fail
afterwards — either:

- git's atomic ref protection rejects it (`remote rejected` / `cannot lock
  ref`), because the inner push already advanced the ref, or
- the connection drops during the long CI watch (`Connection closed` /
  SIGPIPE).

**Both are expected and harmless** — the push already succeeded via the inner
push. Do **not** interpret the outer push's non-zero exit code, the
`remote rejected` line, or the `connection closed` line as a real failure.

**Rely on the hook's own report instead:**

- `post-push: ✔ push succeeded` — the branch was pushed.
- The boxed `✓ all CI checks passed` / `✗ CI FAILED` block — the real CI
  verdict, printed once CI finishes. Failing jobs are listed with links.
- The review-status reminder line — check it before merging.

If you script around `git push` (CI, automation, `set -e` wrappers), either
disable this hook in that context or ignore the push exit code and parse the
report.

### Configuration (env vars)

CI registration can lag a few seconds after a fresh push, so
`scripts/check-pr-ci.sh` polls until at least one check appears. Tune the poll
with:

| Env var | Default | Meaning |
| --- | --- | --- |
| `PINE_PREPUSH_CHECK_POLL_TRIES` | `24` | Max poll attempts before giving up. |
| `PINE_PREPUSH_CHECK_POLL_INTERVAL` | `5` | Seconds between attempts. |

### Requirements

- `gh` (GitHub CLI), authenticated — used to resolve the PR and watch checks.
  Absent → CI watch is skipped (non-fatal; the push still happens).
- `jq` — used to parse the check list. Absent → the script fails safe (treats
  it as a CI failure) rather than risking a false green.

### Refspec handling

The inner push mirrors the exact refspecs git hands the hook on stdin, so
force-pushes (`--force-with-lease` is applied automatically for
non-fast-forward updates), tag pushes, multi-ref pushes (`git push --all`) and
ref deletions (`git push origin :branch`) all work. Deletion-only pushes skip
the CI watch entirely.
