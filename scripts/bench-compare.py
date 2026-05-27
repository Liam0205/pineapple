#!/usr/bin/env python3
"""Compare two cross-runtime benchmark reports and output a delta summary."""
import argparse
import re
import sys
from pathlib import Path


def parse_report(text: str) -> dict[str, dict[tuple[str, int], dict[str, float]]]:
    """Parse report into {phase: {(runtime, nodes): {metric: value}}}."""
    phases: dict[str, dict[tuple[str, int], dict[str, float]]] = {}
    current_phase = None

    for line in text.splitlines():
        if line.strip().startswith("── Phase"):
            m = re.search(r"Phase (\d+):", line)
            if m:
                current_phase = f"phase{m.group(1)}"
                phases[current_phase] = {}
            continue

        if current_phase is None:
            continue

        # Data lines: "  runtime  nodes  qps  mean  stddev  p50  p90  p99"
        parts = line.split()
        if len(parts) == 8:
            runtime = parts[0]
            try:
                nodes = int(parts[1])
                qps = float(parts[2])
                mean = float(parts[3])
                p50 = float(parts[5])
                p90 = float(parts[6])
                p99 = float(parts[7])
            except (ValueError, IndexError):
                continue
            phases[current_phase][(runtime, nodes)] = {
                "qps": qps,
                "mean": mean,
                "p50": p50,
                "p90": p90,
                "p99": p99,
            }

    return phases


def pct_change(old: float, new: float) -> str:
    if old == 0:
        return "N/A"
    delta = (new - old) / old * 100
    sign = "+" if delta >= 0 else ""
    return f"{sign}{delta:.1f}%"


def format_comparison(prev: dict, curr: dict) -> str:
    lines = []
    phase_names = {"phase1": "Single-request latency", "phase2": "QPS=500 latency", "phase3": "Max throughput"}

    for phase_key in sorted(set(prev.keys()) | set(curr.keys())):
        phase_label = phase_names.get(phase_key, phase_key)
        prev_data = prev.get(phase_key, {})
        curr_data = curr.get(phase_key, {})

        all_keys = sorted(set(prev_data.keys()) | set(curr_data.keys()), key=lambda k: (k[0], k[1]))
        if not all_keys:
            continue

        lines.append(f"── {phase_label} (QPS: higher=better, latency: lower=better) ──")
        lines.append(f"  {'Runtime':<8} {'Nodes':>5}  {'QPS Δ':>10}  {'Mean Δ':>10}  {'P50 Δ':>10}  {'P99 Δ':>10}")
        lines.append(f"  {'-------':<8} {'-----':>5}  {'----------':>10}  {'----------':>10}  {'----------':>10}  {'----------':>10}")

        for key in all_keys:
            runtime, nodes = key
            if key not in prev_data or key not in curr_data:
                continue
            p = prev_data[key]
            c = curr_data[key]
            qps_d = pct_change(p["qps"], c["qps"])
            mean_d = pct_change(p["mean"], c["mean"])
            p50_d = pct_change(p["p50"], c["p50"])
            p99_d = pct_change(p["p99"], c["p99"])
            lines.append(f"  {runtime:<8} {nodes:>5}  {qps_d:>10}  {mean_d:>10}  {p50_d:>10}  {p99_d:>10}")

        lines.append("")

    if not lines:
        lines.append("No comparable data found between runs.")

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
