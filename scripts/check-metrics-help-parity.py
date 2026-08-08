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

# Scan ALL of pine-go, not just pkg/ and internal/. A first version listed those
# two only and silently missed the Lua-pool and Redis metrics declared under
# operators/ — ten of the repo's metrics, i.e. the check covered 14 of 24 while
# reporting success. Review caught it.
GO_GLOBS = ["pine-go/**/*.go"]
# Headers under pine-cpp/src/ count too: the Redis metrics live in
# src/redis/connection_pool.hpp, which an include/-only glob missed, leaving six
# metrics uncomparable while the summary still said OK.
CPP_GLOBS = ["pine-cpp/src/**/*.cpp", "pine-cpp/src/**/*.hpp", "pine-cpp/include/**/*.hpp"]
JAVA_GLOBS = ["pine-java/src/main/java/**/*.java"]

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


# Metric name followed, within a bounded window, by its bucket array. All three
# languages put the name before the buckets in the same declaration:
#   go    Name: "x", ... Buckets: []float64{...}
#   java  new HistogramOpts("x", "help", new double[]{...}, ...)
#   cpp   {{"x", "help", {labels}}, {...}}
# An earlier version compared only the SET of arrays each runtime declared, on the
# theory that name association was too brittle. That was wrong twice over: the
# association is in fact tractable, and set comparison provably misses a real
# divergence — swapping two metrics' arrays leaves the set identical while giving
# every affected metric the wrong buckets, which was demonstrated to pass green.
NAMED_BUCKET_PAT = re.compile(
    r'"(pine_[a-z_]+)"'          # metric name
    r"(?:.(?!\"pine_[a-z_]+\"))*?"  # anything up to, but not crossing, the next metric name
    r"\{\s*([0-9][0-9.,eE+\-\s]*?)\s*\}",
    re.S,
)


def scan_named_buckets(patterns):
    """Map metric name -> declared bucket tuple, per runtime.

    Only records arrays of four or more numbers so that small literals (label
    lists, single defaults) are not mistaken for bucket sets.
    """
    found = {}
    for pat in patterns:
        for path in glob.glob(pat, recursive=True):
            if "_test" in path or not os.path.isfile(path):
                continue
            text = open(path, encoding="utf-8").read()
            for name, raw in NAMED_BUCKET_PAT.findall(text):
                parts = [x.strip() for x in raw.split(",") if x.strip()]
                try:
                    vals = tuple(float(x) for x in parts)
                except ValueError:
                    continue
                if len(vals) >= 4:
                    found[name] = vals
    return found


def main():
    go = scan(GO_GLOBS, GO_PAT)
    if not go:
        print("FAIL: no pine_* metrics found in pine-go — did the declaration shape change?")
        return 1
    java = scan(JAVA_GLOBS, JAVA_PAT)
    cpp = scan(CPP_GLOBS, CPP_PAT)

    # Fail on ASYMMETRY, not just on zero. An earlier version fail-fasted only when
    # pine-go matched nothing, which let a plausible refactor go unnoticed: rewriting
    # one C++ declaration to use a named MetricOpts variable dropped it from the cpp
    # map, so the comparison simply skipped that metric and still printed OK. Losing
    # a metric from either side must be as loud as a mismatch, because a check that
    # silently compares fewer things is worse than no check — it reads as a pass.
    coverage_bad = False
    for label, other in (("java", java), ("cpp", cpp)):
        unseen = sorted(set(go) - set(other))
        if unseen:
            coverage_bad = True
            print(f"FAIL {label} declares no Help for {len(unseen)} pine-go metric(s):")
            for name in unseen:
                print(f"  {name}")
    if coverage_bad:
        print(
            "\nEvery pine-go metric must be found in both other runtimes, otherwise "
            "this check silently compares a subset. If a runtime genuinely does not "
            "declare a metric, add it to the exemption list in this script with a "
            "reason rather than letting the scan miss it."
        )
        return 1

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
    # Bucket arrays, keyed by metric name so a cross-assignment cannot hide.
    gb = scan_named_buckets(GO_GLOBS)
    jb = scan_named_buckets(JAVA_GLOBS)
    cb = scan_named_buckets(CPP_GLOBS)
    bucket_bad = False
    for label, other in (("java", jb), ("cpp", cb)):
        for name, want in sorted(gb.items()):
            if name in other and other[name] != want:
                bucket_bad = True
                print(f"FAIL buckets {name} [{label}]")
                print(f"  pine-go: {list(want)}")
                print(f"  {label:>7}: {list(other[name])}")
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
    print(
        f"OK  histogram buckets: {len(gb)} named arrays in pine-go, "
        f"{len(set(gb) & set(jb))} comparable in java, "
        f"{len(set(gb) & set(cb))} in cpp, 0 mismatches"
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
