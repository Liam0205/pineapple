#!/usr/bin/env bash
#
# bump-version.sh — synchronise version across the Pineapple engine repo.
#
# Usage:
#   bash scripts/bump-version.sh <new-version>
#
# What it does (in order):
#   1.  Updates pine-go/version.go         (Go constant)
#   2.  Updates apple/_version.py          (Python package version)
#   3.  Updates pine-java/pom.xml          (Java Maven artifact version)
#   4.  Updates pine-python/pyproject.toml (Python engine version)
#   5.  Updates pine-cpp kVersion          (C++ constexpr)
#   6.  Updates _PINEAPPLE_VERSION in JSON fixtures, C++ tests, fuzz scripts, and Java examples
#   7.  Runs codegen                       (regenerates apple_generated/ and doc/operators/)
#   8.  Runs Go tests
#   9.  Runs Python tests (apple)
#   10. Runs Pine-Python tests
#   11. Runs Java tests
#   12. Builds and tests pine-cpp
#   13. Runs cross-validation
#
# The script does NOT commit, tag, or push. Review the diff and do that yourself.

set -euo pipefail
shopt -s globstar

if [[ $# -ne 1 ]]; then
  echo "Usage: $0 <new-version>"
  exit 1
fi

NEW_VERSION="$1"

# Validate version format (semver-ish: digits.digits.digits, optional pre-release).
if ! [[ "$NEW_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
  echo "Error: version must be in semver format (e.g. 0.9.0 or 1.0.0-rc1)"
  exit 1
fi

# Resolve repo root (script lives in scripts/).
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

echo "==> Bumping Pineapple to v${NEW_VERSION}"
echo

# --- 1. version.go ---
echo "[1/13] Updating pine-go/version.go"
perl -0pi -e "s/const Version = \".*\"/const Version = \"${NEW_VERSION}\"/" pine-go/version.go

# --- 2. apple/_version.py ---
echo "[2/13] Updating apple/_version.py"
perl -0pi -e "s/__version__ = \".*\"/__version__ = \"${NEW_VERSION}\"/" apple/_version.py

# --- 3. pine-java/pom.xml ---
echo "[3/13] Updating pine-java/pom.xml"
perl -0pi -e "s|<version>[^<]+</version>(\\s*<packaging>jar</packaging>)|<version>${NEW_VERSION}</version>\$1|" pine-java/pom.xml

# --- 4. pine-python/pyproject.toml ---
echo "[4/13] Updating pine-python/pyproject.toml"
perl -pi -e "s/^version = \".*\"/version = \"${NEW_VERSION}\"/" pine-python/pyproject.toml

# --- 5. pine-cpp/include/pine/pine.hpp ---
echo "[5/13] Updating pine-cpp kVersion"
perl -pi -e "s/inline constexpr const char\\* kVersion = \".*\"/inline constexpr const char* kVersion = \"${NEW_VERSION}\"/" pine-cpp/include/pine/pine.hpp

# --- 6. JSON fixtures, C++ tests, fuzz scripts, and Java examples ---
echo "[6/13] Updating _PINEAPPLE_VERSION in fixtures, tests, and examples"
updated_files=()
for f in pipeline.json pine-go/testdata/*.json fixtures/**/*.json pine-cpp/tests/*.cpp scripts/differential-fuzz.py scripts/dag-differential-fuzz.py; do
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

# --- 7. Codegen ---
echo "[7/13] Running codegen"
(cd pine-go && go run ./cmd/pineapple-codegen -output ../apple_generated -doc-dir ../doc/operators -operators-dir operators)

# --- 8. Go tests ---
echo "[8/13] Running Go tests"
(cd pine-go && go test ./...)

# --- 9. Python tests (apple) ---
echo "[9/13] Running Python tests (apple)"
python3 -m pytest apple/tests/ -v

# --- 10. Pine-Python tests ---
echo "[10/13] Running Pine-Python tests"
(cd pine-python && python3 -m pytest tests/ -v)

# --- 11. Java tests ---
echo "[11/13] Running Java tests"
(cd pine-java && mvn test -B -q)

# --- 12. C++ build + test ---
echo "[12/13] Building and testing pine-cpp"
(cd pine-cpp && cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DPINE_CPP_BUILD_TESTS=ON && cmake --build build -j2 && ./build/pine_cpp_tests)

# --- 13. Cross-validation ---
echo "[13/13] Running cross-validation"
bash scripts/cross-validate.sh

echo
echo "==> Done. Version bumped to ${NEW_VERSION}."
echo "    Review the diff, then commit + tag + push:"
echo "      git add -A && git commit -m 'bump: v${NEW_VERSION}'"
echo "      git tag v${NEW_VERSION} && git push origin master --tags"
