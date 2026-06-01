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
#   4.  Updates pine-cpp kVersion          (C++ constexpr)
#   5.  Updates _PINEAPPLE_VERSION in JSON fixtures, C++ tests, fuzz scripts, and Java examples
#   6.  Runs codegen                       (regenerates apple_generated/ and doc/operators/)
#   7.  Runs Go tests
#   8.  Runs Python tests (apple)
#   9.  Runs Java tests
#   10. Builds and tests pine-cpp
#   11. Runs cross-validation
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
echo "[1/11] Updating pine-go/version.go"
perl -0pi -e "s/const Version = \".*\"/const Version = \"${NEW_VERSION}\"/" pine-go/version.go

# --- 2. apple/_version.py ---
echo "[2/11] Updating apple/_version.py"
perl -0pi -e "s/__version__ = \".*\"/__version__ = \"${NEW_VERSION}\"/" apple/_version.py

# --- 3. pine-java/pom.xml ---
echo "[3/11] Updating pine-java/pom.xml"
perl -0pi -e "s|<version>[^<]+</version>(\\s*<packaging>jar</packaging>)|<version>${NEW_VERSION}</version>\$1|" pine-java/pom.xml

# --- 4. pine-cpp/include/pine/pine.hpp ---
echo "[4/11] Updating pine-cpp kVersion"
perl -pi -e "s/inline constexpr const char\\* kVersion = \".*\"/inline constexpr const char* kVersion = \"${NEW_VERSION}\"/" pine-cpp/include/pine/pine.hpp

# --- 5. JSON fixtures, C++ tests, fuzz scripts, and Java examples ---
echo "[5/11] Updating _PINEAPPLE_VERSION in fixtures, tests, and examples"
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

# --- 6. Codegen ---
echo "[6/11] Running codegen"
(cd pine-go && go run ./cmd/pineapple-codegen -output ../apple_generated -doc-dir ../doc/operators -operators-dir operators)

# --- 7. Go tests ---
echo "[7/11] Running Go tests"
(cd pine-go && go test ./...)

# --- 8. Python tests (apple) ---
echo "[8/11] Running Python tests (apple)"
python3 -m pytest apple/tests/ -v

# --- 9. Java tests ---
echo "[9/11] Running Java tests"
(cd pine-java && mvn test -B -q)

# --- 10. C++ build + test ---
echo "[10/11] Building and testing pine-cpp"
(cd pine-cpp && cmake -S . -B build -DCMAKE_BUILD_TYPE=Release -DPINE_CPP_BUILD_TESTS=ON -DPINE_BUILD_BENCH_STUBS=OFF -DCMAKE_POLICY_VERSION_MINIMUM=3.5 && cmake --build build -j2 && ./build/pine_cpp_tests)

# --- 11. Cross-validation ---
echo "[11/11] Running cross-validation"
bash scripts/cross-validate.sh

echo
echo "==> Done. Version bumped to ${NEW_VERSION}."
echo "    Review the diff, then commit + tag + push:"
echo "      git add -A && git commit -m 'bump: v${NEW_VERSION}'"
echo "      git tag v${NEW_VERSION} && git push origin master --tags"
