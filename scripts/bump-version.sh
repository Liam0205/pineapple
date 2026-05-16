#!/usr/bin/env bash
#
# bump-version.sh — synchronise version across the Pineapple engine repo.
#
# Usage:
#   bash scripts/bump-version.sh 0.3.0
#
# What it does (in order):
#   1. Updates pine-go/version.go    (Go constant)
#   2. Updates apple/_version.py     (Python package version)
#   3. Updates pine-java/pom.xml     (Java Maven artifact version)
#   4. Updates _PINEAPPLE_VERSION in JSON fixtures and Java examples
#   5. Runs codegen                  (regenerates apple_generated/ and doc/operators/)
#   6. Runs Go tests                 (go test ./...)
#   7. Runs Python tests             (python3 -m pytest apple/tests/ -v)
#   8. Runs Java tests               (mvn test in pine-java/)
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
echo "[1/8] Updating pine-go/version.go"
perl -0pi -e "s/const Version = \".*\"/const Version = \"${NEW_VERSION}\"/" pine-go/version.go

# --- 2. apple/_version.py ---
echo "[2/8] Updating apple/_version.py"
perl -0pi -e "s/__version__ = \".*\"/__version__ = \"${NEW_VERSION}\"/" apple/_version.py

# --- 3. pine-java/pom.xml ---
echo "[3/8] Updating pine-java/pom.xml"
perl -0pi -e "s|<version>[^<]+</version>(\\s*<packaging>jar</packaging>)|<version>${NEW_VERSION}</version>\$1|" pine-java/pom.xml

# --- 4. JSON fixtures and examples ---
echo "[4/8] Updating _PINEAPPLE_VERSION in fixtures and examples"
updated_files=()
for f in pipeline.json pine-go/testdata/*.json fixtures/**/*.json; do
  [[ -f "$f" ]] || continue
  if grep -q '"_PINEAPPLE_VERSION"' "$f"; then
    perl -0pi -e "s/\"_PINEAPPLE_VERSION\": \"[^\"]*\"/\"_PINEAPPLE_VERSION\": \"${NEW_VERSION}\"/" "$f"
    updated_files+=("$f")
  fi
done
# Java source files use escaped quotes: \"_PINEAPPLE_VERSION\"
for f in pine-java/examples/*.java; do
  [[ -f "$f" ]] || continue
  if grep -q '_PINEAPPLE_VERSION' "$f"; then
    perl -pi -e 's/\\"_PINEAPPLE_VERSION\\": \\"[^\\]*\\"/\\"_PINEAPPLE_VERSION\\": \\"'"${NEW_VERSION}"'\\"/' "$f"
    updated_files+=("$f")
  fi
done
if [[ ${#updated_files[@]} -gt 0 ]]; then
  printf "  updated: %s\n" "${updated_files[@]}"
else
  echo "  (no files with _PINEAPPLE_VERSION found)"
fi

# --- 5. Codegen ---
echo "[5/8] Running codegen"
(cd pine-go && go run ./cmd/pineapple-codegen -output ../apple_generated -doc-dir ../doc/operators -operators-dir operators)

# --- 6. Go tests ---
echo "[6/8] Running Go tests"
(cd pine-go && go test ./...)

# --- 7. Python tests ---
echo "[7/8] Running Python tests"
python3 -m pytest apple/tests/ -v

# --- 8. Java tests ---
echo "[8/8] Running Java tests"
(cd pine-java && mvn test -B -q)

echo
echo "==> Done. Version bumped to ${NEW_VERSION}."
echo "    Review the diff, then commit + tag + push:"
echo "      git add -A && git commit -m 'bump: v${NEW_VERSION}'"
echo "      git tag v${NEW_VERSION} && git push origin master --tags"
