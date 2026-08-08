#!/usr/bin/env python3
"""Verify that every pine_* metric's Help text matches across the three runtimes.

Why this exists: issue #193 found that ALL nine engine metrics plus five
server-layer ones had different Help text in pine-java, and one differed in
pine-cpp, while a doc claimed cross-validate guaranteed metric parity. It did
not — `# HELP` never reaches any output (the repo emits no Prometheus text
format and no bundled Provider reads the field), so nothing could catch drift.

Rather than assert nothing is wrong, this pins it. pine-go is the source of
truth, matching the repo's codegen convention.
"""
import glob
import os
import re
import sys

GO_PAT = re.compile(r'Name:\s*"(pine_[a-z_]+)",\s*\n?\s*Help:\s*"([^"]*)"')
JAVA_PAT = re.compile(r'"(pine_[a-z_]+)",\s*"([^"]*)"')
CPP_PAT = re.compile(r'\{"(pine_[a-z_]+)",\s*"([^"]*)"')


def scan(patterns, pattern):
    found = {}
    for pat in patterns:
        for path in glob.glob(pat, recursive=True):
            if "_test" in path or not os.path.isfile(path):
                continue
            for name, help_text in pattern.findall(open(path, encoding="utf-8").read()):
                found[name] = help_text
    return found


def main():
    go = scan(["pine-go/pkg/**/*.go", "pine-go/internal/**/*.go"], GO_PAT)
    if not go:
        print("FAIL: no pine_* metrics found in pine-go — did the declaration shape change?")
        return 1
    java = scan(["pine-java/src/main/java/**/*.java"], JAVA_PAT)
    cpp = scan(["pine-cpp/src/**/*.cpp", "pine-cpp/include/**/*.hpp"], CPP_PAT)

    bad = []
    for name, want in sorted(go.items()):
        for label, other in (("java", java), ("cpp", cpp)):
            if name in other and other[name] != want:
                bad.append((name, label, want, other[name]))

    for name, label, want, got in bad:
        print(f"FAIL {name} [{label}]\n  pine-go: {want!r}\n  {label:>7}: {got!r}")
    if bad:
        print(
            f"\n{len(bad)} Help mismatch(es). pine-go is the source of truth; "
            "align the other side."
        )
        return 1
    print(
        f"OK  metrics Help parity: {len(go)} pine-go metrics, "
        f"{len(set(go) & set(java))} comparable in java, "
        f"{len(set(go) & set(cpp))} in cpp, 0 mismatches"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
