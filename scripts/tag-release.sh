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
V_PYPROJ=$(grep -oP '^version = "\K[^"]+' pine-python/pyproject.toml)

echo "Collected versions:"
echo "  pine-go/version.go       → $V_GO"
echo "  apple/_version.py        → $V_PY"
echo "  pine-java/pom.xml        → $V_JAVA"
echo "  pine-python/pyproject.toml → $V_PYPROJ"

# --- Validate consistency ---

if [[ "$V_GO" != "$V_PY" || "$V_GO" != "$V_JAVA" || "$V_GO" != "$V_PYPROJ" ]]; then
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

push_tag() {
  local tag="$1"
  if git push origin "$tag" 2>/dev/null; then
    echo "  pushed $tag"
  else
    echo "  [WARN] normal push failed for $tag, retrying with --force"
    git push origin "$tag" --force
    echo "  pushed $tag (force)"
  fi
}

echo "Pushing tags..."
push_tag "$TAG_ROOT"
push_tag "$TAG_GO"

echo
echo "Done. Released v${VERSION}."
