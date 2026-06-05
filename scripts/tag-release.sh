#!/usr/bin/env bash
# tag-release.sh — collect version from source files, validate consistency,
# create vX.Y.Z + pine-go/vX.Y.Z tags, and push them.
#
# Usage:
#   bash scripts/tag-release.sh

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# --- Collect versions from the 4 authoritative source files ---

V_GO=$(grep -oP 'const Version = "\K[^"]+' pine-go/version.go)
V_PY=$(grep -oP '__version__ = "\K[^"]+' apple/_version.py)
V_JAVA=$(grep -m1 -oP '(?<=<version>)[^<]+' pine-java/pom.xml)
V_CPP=$(grep -oP 'kVersion = "\K[^"]+' pine-cpp/include/pine/pine.hpp)

echo "Collected versions:"
echo "  pine-go/version.go         → $V_GO"
echo "  apple/_version.py          → $V_PY"
echo "  pine-java/pom.xml          → $V_JAVA"
echo "  pine-cpp/pine.hpp          → $V_CPP"

# --- Validate consistency ---

if [[ "$V_GO" != "$V_PY" || "$V_GO" != "$V_JAVA" || "$V_GO" != "$V_CPP" ]]; then
  echo
  echo "ERROR: version mismatch detected!"
  echo "  Use 'bash scripts/bump-version.sh <version>' to synchronize all files."
  exit 1
fi

VERSION="$V_GO"
TAG_ROOT="v${VERSION}"
TAG_GO="pine-go/v${VERSION}"

echo
echo "Version consistent: $VERSION"
echo "Tags to create: $TAG_ROOT, $TAG_GO"
echo

# --- Create tags (force-replace if already exist) ---

create_tag() {
  local tag="$1"
  if git tag "$tag" 2>/dev/null; then
    echo "  created $tag"
  else
    echo "  [WARN] tag $tag already exists, replacing"
    git tag -f "$tag"
    echo "  created $tag (force)"
  fi
}

create_tag "$TAG_ROOT"
create_tag "$TAG_GO"

# --- Push tags ---
#
# Release tags are immutable contracts: once pushed, downstream consumers
# (pkg.go.dev, Maven Central, PyPI mirrors) cache the tag → commit mapping
# by SHA. Silently moving a remote tag to a different commit is a
# semantic break, even when it appears to "succeed".
#
# So we do NOT trust `git push`'s exit code as the only signal — the
# pre-push hook in this repo can produce non-zero outer rc even on a
# successful inner push, which would have made the old fallback to
# `--force` fire on every release. Instead we inspect the remote state
# first via `ls-remote` and decide deterministically.

push_tag() {
  local tag="$1"

  # Resolve the local commit the tag points at. Use `^{commit}` so this
  # also works if the tag is later switched to an annotated form.
  local local_sha
  local_sha="$(git rev-parse "${tag}^{commit}")"

  # Look up the remote tag, preferring the dereferenced commit SHA
  # (annotated tag) over the raw tag-object SHA (lightweight tag).
  local remote_sha
  remote_sha="$(git ls-remote origin "refs/tags/${tag}^{}" | awk '{print $1}')"
  if [ -z "$remote_sha" ]; then
    remote_sha="$(git ls-remote origin "refs/tags/${tag}" | awk '{print $1}')"
  fi

  if [ -z "$remote_sha" ]; then
    echo "  pushing $tag (new)..."
    # The repo's pre-push hook self-wraps the push: an inner push performs
    # the real ref update, after which the outer `git push` we run here can
    # exit non-zero even though the ref landed (the OUTER push sees a stale
    # zero→SHA expectation against a now-already-at-SHA remote). Under the
    # script-level `set -e`, that non-zero rc would abort the script after
    # the first tag, leaving TAG_GO unpushed and breaking the dual-tag
    # contract. So tolerate non-zero rc and verify the actual remote state
    # via ls-remote — same pattern the already-exists branch below uses.
    git push origin "$tag" || true
    local pushed_sha
    pushed_sha="$(git ls-remote origin "refs/tags/${tag}^{}" | awk '{print $1}')"
    if [ -z "$pushed_sha" ]; then
      pushed_sha="$(git ls-remote origin "refs/tags/${tag}" | awk '{print $1}')"
    fi
    if [ "$pushed_sha" = "$local_sha" ]; then
      echo "  pushed $tag (remote verified at $local_sha)"
      return 0
    fi
    echo "  [ERROR] $tag did not land on remote: expected $local_sha, got '${pushed_sha:-<none>}'" >&2
    exit 1
  fi

  if [ "$remote_sha" = "$local_sha" ]; then
    echo "  $tag already on remote at the same commit — skipping."
    return 0
  fi

  cat >&2 <<EOF

  [ERROR] tag $tag already exists on remote at a different commit:
            local:  $local_sha
            remote: $remote_sha

          A release tag must not be moved silently — downstream consumers
          (pkg.go.dev, Maven Central, PyPI mirrors) cache release artifacts
          by SHA, and a force-push here will silently divert future fetches
          to a different tree.

          If this move is genuinely intended, delete the remote tag first
          and re-run this script:
            git push origin :refs/tags/$tag

EOF
  exit 1
}

echo "Pushing tags..."
push_tag "$TAG_ROOT"
push_tag "$TAG_GO"

echo
echo "Done. Released v${VERSION}."
