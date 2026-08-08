#!/usr/bin/env python3
"""Verify that every pine_* metric's Help text matches across the three runtimes.

Why this exists: issue #193 found that ALL nine engine metrics plus five
server-layer ones had different Help text in pine-java, and one differed in
pine-cpp, while a doc claimed cross-validate guaranteed metric parity. It did
not — `# HELP` never reaches any output (the repo emits no Prometheus text
format and no bundled Provider reads the field), so nothing could catch drift.

Rather than assert nothing is wrong, this pins it. pine-go is the source of
truth, matching the repo's codegen convention.

It also checks histogram bucket boundaries, for the same reason and at the same
near-zero cost: no bundled Provider reads HistogramOpts.Buckets, so the three
runtimes' bucket arrays agreeing was pure coincidence with nothing to catch
drift. Buckets are only a suggestion to a downstream backend, but a suggestion
that differs per runtime makes downstream quantiles incomparable across them.
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


BUCKET_PAT = re.compile(r"\{\s*(0?\.\d[0-9.,\s]*?|[0-9][0-9.,\s]*?)\s*\}")


def scan_buckets(patterns):
    """Collect every bucket array literal per file, normalised to float lists.

    Deliberately compares the SET of arrays a runtime declares rather than
    mapping each to its metric name: the three languages spell the declaration
    differently enough that name association is brittle, while "does this
    runtime declare the same collection of bucket arrays" catches drift just as
    well and cannot silently mis-associate.
    """
    found = set()
    for pat in patterns:
        for path in glob.glob(pat, recursive=True):
            if "_test" in path or not os.path.isfile(path):
                continue
            for raw in BUCKET_PAT.findall(open(path, encoding="utf-8").read()):
                parts = [x.strip() for x in raw.split(",") if x.strip()]
                try:
                    vals = tuple(float(x) for x in parts)
                except ValueError:
                    continue
                if len(vals) >= 4:  # skip small literals that are not bucket sets
                    found.add(vals)
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
    # Bucket arrays: compare the collection each runtime declares.
    gb = scan_buckets(["pine-go/pkg/**/*.go", "pine-go/internal/**/*.go"])
    jb = scan_buckets(["pine-java/src/main/java/**/*.java"])
    cb = scan_buckets(["pine-cpp/src/**/*.cpp", "pine-cpp/include/**/*.hpp"])
    bucket_bad = False
    for label, other in (("java", jb), ("cpp", cb)):
        missing = gb - other
        extra = other - gb
        # Only report arrays the other side declares differently, not ones it
        # simply does not declare: not every runtime instruments every histogram.
        if missing and extra:
            bucket_bad = True
            print(f"FAIL histogram buckets [{label}] differ from pine-go")
            for v in sorted(extra):
                print(f"  only in {label}: {list(v)}")
            for v in sorted(missing):
                print(f"  only in pine-go: {list(v)}")
    if bucket_bad:
        print(
            "\nBucket arrays are a suggestion to downstream backends, but one that "
            "differs per runtime makes downstream quantiles incomparable. Align to "
            "pine-go."
        )
        return 1

    print(
        f"OK  metrics Help parity: {len(go)} pine-go metrics, "
        f"{len(set(go) & set(java))} comparable in java, "
        f"{len(set(go) & set(cpp))} in cpp, 0 mismatches"
    )
    print(f"OK  histogram buckets: {len(gb)} distinct arrays in pine-go, no cross-runtime drift")
    return 0


if __name__ == "__main__":
    sys.exit(main())
