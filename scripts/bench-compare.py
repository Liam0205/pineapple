#!/usr/bin/env python3
"""Compare two cross-runtime benchmark reports and output a delta summary."""
import argparse
from pathlib import Path


def parse_report(text: str) -> dict[tuple, dict[str, float]]:
    """Parse report into {(runtime, fixture, storage): {metric: value}}.

    Handles both the current 9-column layout emitted by
    bench-cross-runtime.sh and the legacy 11-column layout, normalizing the
    legacy (nodes, par, op) triple into a single synthetic fixture name so a
    report from either era compares against the other. This mirrors
    bench-analyze.py's parse_report; the two must stay in sync, since a
    format the analyzer accepts but the comparer silently drops looks
    exactly like "no regressions" in CI.
    """
    data: dict[tuple, dict[str, float]] = {}

    for line in text.splitlines():
        parts = line.split()
        if not parts:
            continue
        # Skip header/separator lines
        # startswith, not equality: the banner rule is one long run of box
        # characters, so `parts[0] == "═══"` never matched. Harmless before
        # (the line has neither 9 nor 11 fields and fell through) but it read
        # like a working guard.
        #
        # WARNING: is listed explicitly because the shortfall notice
        # bench-cross-runtime.sh appends splits into exactly 9 fields, the same
        # width as a data row. Today it is rejected only because field 4 is not
        # a float; rewording it could silently turn it into a data row.
        if parts[0] in ("Runtime", "-------", "WARNING:", "skipped:") or parts[0].startswith("═"):
            continue
        if len(parts) == 9:
            runtime, fixture, storage = parts[0], parts[1], parts[2]
            metrics = parts[3:]
        elif len(parts) == 11:
            runtime, storage = parts[0], parts[2]
            fixture = f"{parts[4]}_n{parts[1]}"
            metrics = parts[5:]
        else:
            continue
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
