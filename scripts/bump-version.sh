#!/usr/bin/env bash
#
# bump-version.sh — synchronise version across the Pineapple engine repo.
#
# Usage:
#   bash scripts/bump-version.sh 0.3.0
#
# What it does (in order):
#   1. Updates version.go        (Go constant)
#   2. Updates apple/_version.py (Python package version)
#   3. Updates _PINEAPPLE_VERSION in all JSON fixtures (testdata/*.json, pipeline.json)
#   4. Runs codegen              (regenerates apple_generated/ and doc/operators/)
#   5. Runs Go tests             (go test ./...)
#   6. Runs Python tests         (python3 -m pytest apple/tests/ -v)
#
# The script does NOT commit, tag, or push. Review the diff and do that yourself.

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <new-version>"
  echo "Example: $0 0.3.0"
  exit 1
fi

NEW_VERSION="$1"

# Validate version format (semver-ish: digits.digits.digits, optional pre-release).
if ! [[ "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
  echo "Error: version must be in semver format (e.g. 0.3.0 or 1.0.0-rc1)"
  exit 1
fi

# Resolve repo root (script lives in scripts/).
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

echo "==> Bumping Pineapple to v${NEW_VERSION}"
echo

# --- 1. version.go ---
echo "[1/6] Updating version.go"
sed -i '' "s/const Version = \".*\"/const Version = \"${NEW_VERSION}\"/" version.go

# --- 2. apple/_version.py ---
echo "[2/6] Updating apple/_version.py"
sed -i '' "s/__version__ = \".*\"/__version__ = \"${NEW_VERSION}\"/" apple/_version.py

# --- 3. JSON fixtures ---
echo "[3/6] Updating _PINEAPPLE_VERSION in JSON fixtures"
json_files=()
for f in pipeline.json testdata/*.json; do
  [[ -f "$f" ]] || continue
  if grep -q '"_PINEAPPLE_VERSION"' "$f"; then
    sed -i '' "s/\"_PINEAPPLE_VERSION\": \"[^\"]*\"/\"_PINEAPPLE_VERSION\": \"${NEW_VERSION}\"/" "$f"
    json_files+=("$f")
  fi
done
if [[ ${#json_files[@]} -gt 0 ]]; then
  printf "  updated: %s\n" "${json_files[@]}"
else
  echo "  (no JSON files with _PINEAPPLE_VERSION found)"
fi

# --- 4. Codegen ---
echo "[4/6] Running codegen"
go run ./cmd/pineapple-codegen -output apple_generated -doc-dir doc/operators -operators-dir operators

# --- 5. Go tests ---
echo "[5/6] Running Go tests"
go test ./...

# --- 6. Python tests ---
echo "[6/6] Running Python tests"
python3 -m pytest apple/tests/ -v

echo
echo "==> Done. Version bumped to ${NEW_VERSION}."
echo "    Review the diff, then commit + tag + push:"
echo "      git add -A && git commit -m 'bump: v${NEW_VERSION}'"
echo "      git tag v${NEW_VERSION} && git push origin master --tags"
