#!/usr/bin/env python3
"""Compare two cross-runtime benchmark reports and output a delta summary."""
import argparse
from pathlib import Path


def parse_report(text: str) -> dict[tuple, dict[str, float]]:
    """Parse report into {(runtime, fixture, storage): {metric: value}}.

    Reads the 9-column layout emitted by bench-cross-runtime.sh. The line-skip
    rules must stay in sync with bench-analyze.py's parse_report, since a line
    the analyzer ignores but the comparer parses (or vice versa) looks exactly
    like "no regressions" in CI.

    Column handling is deliberately NOT symmetric: bench-analyze.py still
    accepts the legacy 11-column layout because it is run by hand on archived
    reports, whereas this script only ever compares two runs and so can never
    receive one.

    The 11-column layout this used to also accept is gone: nothing has emitted
    it since 2026-05-28, the nightly workflow only ever diffs against the
    previous run's artifact, and artifacts are kept 30 days — so no comparison
    could reach that branch. It was also untested and would have rotted
    silently. Old reports can still be read with the script version from that
    era via git history.
    """
    data: dict[tuple, dict[str, float]] = {}

    for line in text.splitlines():
        parts = line.split()
        if not parts:
            continue
        # Skip header/separator lines
        # '#' covers the shortfall notice bench-cross-runtime.sh appends: it
        # splits into exactly 9 fields, the same width as a data row, so it has
        # to be excluded structurally, not by matching its wording. startswith
        # for the banner because that rule is one long run of box characters
        # (`parts[0] == "═══"` never matched). Kept in sync with
        # bench-analyze.py's parse_report.
        if parts[0].startswith("#") or parts[0].startswith("═"):
            continue
        if parts[0] in ("Runtime", "-------"):
            continue
        if len(parts) != 9:
            continue
        runtime, fixture, storage = parts[0], parts[1], parts[2]
        metrics = parts[3:]
        try:
            qps, mean, stddev, p50, p90, p99 = (float(v) for v in metrics)
        except ValueError:
            continue
        data[(runtime, fixture, storage)] = {
            "qps": qps, "mean": mean, "stddev": stddev,
            "p50": p50, "p90": p90, "p99": p99,
        }

    return data


def pct_change(old: float, new: float) -> str:
    if old == 0:
        return "N/A"
    delta = (new - old) / old * 100
    sign = "+" if delta >= 0 else ""
    return f"{sign}{delta:.1f}%"


def format_comparison(prev: dict, curr: dict) -> str:
    lines = []
    common_keys = sorted(
        set(prev.keys()) & set(curr.keys()),
        key=lambda k: (k[1], k[2], k[0]),  # fixture, storage, runtime
    )

    if not common_keys:
        return "No comparable data found between runs."

    lines.append("── Delta: current vs previous (QPS: higher=better, latency: lower=better) ──")
    lines.append(
        f"  {'Runtime':<8} {'Fixture':<35} {'Stor':>7}"
        f"  {'QPS Δ':>10}  {'Mean Δ':>10}  {'P50 Δ':>10}  {'P99 Δ':>10}"
    )
    lines.append(
        f"  {'-------':<8} {'-' * 35:<35} {'-------':>7}"
        f"  {'----------':>10}  {'----------':>10}  {'----------':>10}  {'----------':>10}"
    )

    for key in common_keys:
        runtime, fixture, storage = key
        p = prev[key]
        c = curr[key]
        qps_d = pct_change(p["qps"], c["qps"])
        mean_d = pct_change(p["mean"], c["mean"])
        p50_d = pct_change(p["p50"], c["p50"])
        p99_d = pct_change(p["p99"], c["p99"])
        lines.append(
            f"  {runtime:<8} {fixture:<35} {storage:>7}"
            f"  {qps_d:>10}  {mean_d:>10}  {p50_d:>10}  {p99_d:>10}"
        )

    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Compare benchmark reports")
    parser.add_argument("--prev", required=True, help="Previous report.txt")
    parser.add_argument("--curr", required=True, help="Current report.txt")
    parser.add_argument("--output", required=True, help="Output comparison file")
    args = parser.parse_args()

    prev_text = Path(args.prev).read_text()
    curr_text = Path(args.curr).read_text()

    prev_data = parse_report(prev_text)
    curr_data = parse_report(curr_text)

    comparison = format_comparison(prev_data, curr_data)

    Path(args.output).write_text(comparison)
    print(comparison)


if __name__ == "__main__":
    main()
