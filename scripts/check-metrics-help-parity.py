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
# Names allow digits: p99 / 5xx / 2xx style metrics are conventional, and an
# `[a-z_]`-only pattern would make such a metric invisible in every runtime at once
# while the count floor (a `<` test) stayed satisfied.
GO_GLOBS = ["pine-go/**/*.go"]
# Headers under pine-cpp/src/ count too: the Redis metrics live in
# src/redis/connection_pool.hpp, which an include/-only glob missed, leaving six
# metrics uncomparable while the summary still said OK.
CPP_GLOBS = ["pine-cpp/src/**/*.cpp", "pine-cpp/src/**/*.hpp", "pine-cpp/include/**/*.hpp"]
JAVA_GLOBS = ["pine-java/src/main/java/**/*.java"]

# Strip line comments before matching, and allow other fields between Name and
# Help. Requiring them adjacent meant an ordinary clarifying comment between the
# two lines silently dropped that metric from EVERY map at once — the go map is
# the baseline, so a go-side miss is invisible to the symmetry guard. Measured: a
# comment plus a real java divergence reported "23 ... 0 mismatches", exit 0.
# Strip line AND block comments, anywhere on a line. Anchoring at line start missed
# trailing comments and Javadoc/`/* */` blocks, which matters in both directions: a
# comment BEFORE a declaration can supply the array the pattern binds to (masking a
# real drift with the correct old value), and one AFTER can overwrite the live value
# because the map keeps the last match. Both were demonstrated.
COMMENT_PAT = re.compile(r"//[^\n]*|/\*[\s\S]*?\*/")
# Help may be written as SEVERAL concatenated literals. Capturing one literal made
# the comparison a PREFIX comparison: a wrapped continuation was dropped, so a real
# divergence read as equal. Not a coverage problem — the metric still counts, so the
# floor and both symmetry guards stay quiet; it defeats the value comparison itself.
# It is also the house style (config/load.go, DataFrame.java, row_frame.cpp all wrap
# this way), and clang-format's BreakStringLiterals pushes C++ there once a line
# passes ColumnLimit. Go and Java join with `+`; C++ juxtaposes adjacent literals.
LITERAL_RUN = r'(?:"(?:[^"\\]|\\.)*"\s*\+?\s*)+'
LITERAL_ONE = re.compile(r'"((?:[^"\\]|\\.)*)"')


def _join_literals(raw):
    """Concatenate a run of adjacent or `+`-joined string literals into one value."""
    return "".join(LITERAL_ONE.findall(raw))


GO_PAT = re.compile(
    r'Name:\s*"(pine_[a-z0-9_]+)"'
    r'(?:(?!Name:\s*")[\s\S])*?'
    r"Help:\s*(" + LITERAL_RUN + r")"
)
JAVA_PAT = re.compile(r'"(pine_[a-z0-9_]+)"\s*,\s*(' + LITERAL_RUN + r")")
CPP_PAT = re.compile(r'\{"(pine_[a-z0-9_]+)"\s*,\s*(' + LITERAL_RUN + r")")


# Anchor every glob at the repo root so the result does not depend on the caller's
# CWD. Running from pine-go/ previously matched nothing and produced a false red.
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def _rooted(patterns):
    return [os.path.join(REPO_ROOT, p) for p in patterns]


def scan(patterns, pattern):
    found = {}
    for pat in _rooted(patterns):
        for path in glob.glob(pat, recursive=True):
            if "_test" in path or not os.path.isfile(path):
                continue
            text = COMMENT_PAT.sub("", open(path, encoding="utf-8").read())
            for name, help_text in pattern.findall(text):
                found[name] = _join_literals(help_text)
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
    r'"(pine_[a-z0-9_]+)"'          # metric name
    r"(?:.(?!\"pine_[a-z0-9_]+\"))*?"  # anything up to, but not crossing, the next metric name
    r"\{\s*([0-9][0-9.,eE+\-\s]*?)\s*\}",
    # NOTE: this binds the next 4+-element numeric brace list before the next
    # pine_* name. It cannot prove the array belongs to that declaration — a
    # bucket-less metric followed by an unrelated numeric literal would bind it.
    # No current declaration triggers that (all associations verified correct), and
    # the alternative is a per-language parser, so this stays a documented limit
    # rather than a silent one. The symmetry check above bounds the damage: a
    # mis-binding shows up as a value mismatch, not as a silent skip.
    re.S,
)


def scan_named_buckets(patterns):
    """Map metric name -> declared bucket tuple, per runtime.

    Only records arrays of four or more numbers so that small literals (label
    lists, single defaults) are not mistaken for bucket sets.
    """
    found = {}
    for pat in _rooted(patterns):
        for path in glob.glob(pat, recursive=True):
            if "_test" in path or not os.path.isfile(path):
                continue
            text = COMMENT_PAT.sub("", open(path, encoding="utf-8").read())
            for name, raw in NAMED_BUCKET_PAT.findall(text):
                parts = [x.strip() for x in raw.split(",") if x.strip()]
                try:
                    vals = tuple(float(x) for x in parts)
                except ValueError:
                    continue
                if len(vals) >= 4:
                    found[name] = vals
    return found


def audit_scan_completeness():
    """Cross-check the regex scan against a crude, independent count.

    Three consecutive review rounds each found a different way to make the pine-go
    scan silently see LESS than the file declares — an inserted comment, a comment
    absorbing a pattern binding, and line-wrapped concatenation. Each was fixed, but
    the shape recurred because a regex scan fails quietly by nature: it reports what
    it matched, never what it should have matched.

    So this counts `"pine_..."` string occurrences with a deliberately dumber method
    and compares. It will not localise a problem, and it is expected to over-count
    (names appear in tests, comments and both sides of a comparison). It only has to
    notice the scan going blind, which no amount of pattern polish can do for itself.
    """
    crude = set()
    for pat in _rooted(GO_GLOBS):
        for path in glob.glob(pat, recursive=True):
            if "_test" in path or not os.path.isfile(path):
                continue
            crude |= set(re.findall(r'"(pine_[a-z0-9_]+)"', open(path, encoding="utf-8").read()))
    return crude


def main():
    go = scan(GO_GLOBS, GO_PAT)
    crude = audit_scan_completeness()
    blind = sorted(crude - set(go))
    if blind:
        print(
            f"FAIL the pine-go scan matched {len(go)} metrics but a cruder count found "
            f"{len(blind)} more name(s) it never parsed:"
        )
        for name in blind:
            print(f"  {name}")
        print(
            "\nThe scan is going blind, which is how this check has been defeated "
            "three separate times. Either GO_PAT no longer matches a declaration shape "
            "used in the tree, or a name appears somewhere this script should ignore. "
            "Fix the pattern rather than the expectation."
        )
        return 1
    # A floor, not just a zero check. Losing ONE metric to a shape change is the
    # dangerous case: the go map is this check's baseline, so a go-side miss removes
    # the metric from every comparison at once and the summary still reads OK. Raise
    # this number when metrics are added; that is the point — it forces a human to
    # notice the count moved.
    EXPECTED_MIN_GO_METRICS = 24
    if len(go) < EXPECTED_MIN_GO_METRICS:
        print(
            f"FAIL: found {len(go)} pine-go metrics, expected at least "
            f"{EXPECTED_MIN_GO_METRICS}. Either a declaration shape stopped matching "
            f"GO_PAT (most likely — this check's baseline silently shrinks), or a "
            f"metric was removed and this floor needs lowering deliberately."
        )
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
        only_other = sorted(set(other) - set(go))
        if only_other:
            coverage_bad = True
            print(f"FAIL {label} declares {len(only_other)} metric(s) pine-go does not:")
            for name in only_other:
                print(f"  {name}")
        unseen = sorted(set(go) - set(other))
        if unseen:
            coverage_bad = True
            print(f"FAIL {label} declares no Help for {len(unseen)} pine-go metric(s):")
            for name in unseen:
                print(f"  {name}")
    if coverage_bad:
        print(
            "Every pine-go metric must be found in both other runtimes, otherwise "
            "this check silently compares a subset. Most often a declaration shape "
            "stopped matching this script's regex — fix the regex. If a runtime "
            "genuinely lacks the metric, that is a real gap to resolve or to record "
            "in llmdoc/memory/doc-gaps.md; there is deliberately no skip mechanism, "
            "because an exemption is how a check rots."
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
        # Symmetry FIRST, same as for Help. `name in other` alone skips any metric
        # whose buckets one runtime declares and another does not, so replacing an
        # array with null/{} passed silently — the only signal was a count in the OK
        # line that nothing asserted on. Both directions matter: a suggestion added
        # to a deliberately-nil histogram is drift too.
        for name in sorted(set(gb) - set(other)):
            bucket_bad = True
            print(f"FAIL buckets {name} [{label}] declares no bucket array")
            print(f"  pine-go: {list(gb[name])}")
        for name in sorted(set(other) - set(gb)):
            bucket_bad = True
            print(f"FAIL buckets {name} [{label}] declares an array pine-go does not")
            print(f"  {label:>7}: {list(other[name])}")
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
