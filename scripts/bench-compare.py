#!/usr/bin/env python3
"""Compare two cross-runtime benchmark reports and output a delta summary."""
import argparse
import re
import sys
from pathlib import Path


def parse_report(text: str) -> dict[tuple, dict[str, float]]:
    """Parse report into {(runtime, nodes, storage, par, op): {metric: value}}."""
    data: dict[tuple, dict[str, float]] = {}

    for line in text.splitlines():
        parts = line.split()
        if len(parts) == 11:
            runtime = parts[0]
            try:
                nodes = int(parts[1])
                storage = parts[2]
                par = int(parts[3])
                op = parts[4]
                qps = float(parts[5])
                mean = float(parts[6])
                stddev = float(parts[7])
                p50 = float(parts[8])
                p90 = float(parts[9])
                p99 = float(parts[10])
            except (ValueError, IndexError):
                continue
            data[(runtime, nodes, storage, par, op)] = {
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
        key=lambda k: (k[4], k[1], k[3], k[2], k[0]),  # op, nodes, par, storage, runtime
    )

    if not common_keys:
        return "No comparable data found between runs."

    lines.append("── Delta: current vs previous (QPS: higher=better, latency: lower=better) ──")
    lines.append(
        f"  {'Runtime':<8} {'Nodes':>5} {'Stor':>6} {'Par':>4} {'Op':>6}"
        f"  {'QPS Δ':>10}  {'Mean Δ':>10}  {'P50 Δ':>10}  {'P99 Δ':>10}"
    )
    lines.append(
        f"  {'-------':<8} {'-----':>5} {'------':>6} {'---':>4} {'------':>6}"
        f"  {'----------':>10}  {'----------':>10}  {'----------':>10}  {'----------':>10}"
    )

    for key in common_keys:
        runtime, nodes, storage, par, op = key
        p = prev[key]
        c = curr[key]
        qps_d = pct_change(p["qps"], c["qps"])
        mean_d = pct_change(p["mean"], c["mean"])
        p50_d = pct_change(p["p50"], c["p50"])
        p99_d = pct_change(p["p99"], c["p99"])
        lines.append(
            f"  {runtime:<8} {nodes:>5} {storage:>6} {par:>4} {op:>6}"
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
