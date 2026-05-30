#!/usr/bin/env python3
"""Analyze a cross-runtime benchmark report and produce a structured summary.

Supports both the current 9-column format (runtime, fixture, storage, qps, mean,
stddev, p50, p90, p99) and the legacy 11-column format. Outputs plain text suitable
for CI logs and Bark notifications.

Usage:
  python3 scripts/bench-analyze.py /tmp/bench_cross_runtime/report.txt
  python3 scripts/bench-analyze.py /tmp/bench_cross_runtime/report.txt --json
  python3 scripts/bench-analyze.py /tmp/bench_cross_runtime/report.txt -o analysis.txt
"""
import argparse
import json as json_mod
import sys
from collections import defaultdict
from pathlib import Path


def parse_report(text: str) -> list[dict]:
    rows = []
    for line in text.splitlines():
        parts = line.split()
        if not parts:
            continue
        # Skip header/separator lines
        if parts[0] in ("Runtime", "-------", "═══"):
            continue
        # Current format: 9 columns
        if len(parts) == 9:
            try:
                rows.append({
                    "runtime": parts[0],
                    "fixture": parts[1],
                    "storage": parts[2],
                    "qps": float(parts[3]),
                    "mean": float(parts[4]),
                    "stddev": float(parts[5]),
                    "p50": float(parts[6]),
                    "p90": float(parts[7]),
                    "p99": float(parts[8]),
                })
            except (ValueError, IndexError):
                continue
        # Legacy format: 11 columns
        elif len(parts) == 11:
            try:
                rows.append({
                    "runtime": parts[0],
                    "fixture": f"{parts[4]}_n{parts[1]}",
                    "storage": parts[2],
                    "qps": float(parts[5]),
                    "mean": float(parts[6]),
                    "stddev": float(parts[7]),
                    "p50": float(parts[8]),
                    "p90": float(parts[9]),
                    "p99": float(parts[10]),
                })
            except (ValueError, IndexError):
                continue
    return rows


def parse_meta(text: str) -> dict:
    meta = {}
    for line in text.splitlines():
        if "Date:" in line:
            meta["date"] = line.split("Date:", 1)[1].strip()
        elif "Machine:" in line:
            meta["machine"] = line.split("Machine:", 1)[1].strip()
        elif "Load:" in line:
            meta["load"] = line.split("Load:", 1)[1].strip()
        elif "Fixtures:" in line:
            meta["fixtures"] = line.split("Fixtures:", 1)[1].strip()
    return meta


def ranking_table(rows: list[dict]) -> str:
    """Per-fixture ranking: who's fastest."""
    lines = []
    by_fixture = defaultdict(list)
    for r in rows:
        by_fixture[(r["fixture"], r["storage"])].append(r)

    for (fixture, storage), group in sorted(by_fixture.items()):
        group.sort(key=lambda x: -x["qps"])
        best_qps = group[0]["qps"]
        lines.append(f"  {fixture} ({storage}):")
        for i, r in enumerate(group):
            ratio = r["qps"] / best_qps if best_qps else 0
            marker = " ★" if i == 0 else ""
            lines.append(
                f"    {i+1}. {r['runtime']:<6} "
                f"QPS={r['qps']:>8.1f}  "
                f"P50={r['p50']*1000:>6.1f}ms  "
                f"P99={r['p99']*1000:>6.1f}ms  "
                f"({ratio:.2f}x){marker}"
            )
    return "\n".join(lines)


def latency_analysis(rows: list[dict]) -> str:
    """Analyze latency distribution characteristics per runtime."""
    lines = []
    by_fixture = defaultdict(list)
    for r in rows:
        by_fixture[(r["fixture"], r["storage"])].append(r)

    for (fixture, storage), group in sorted(by_fixture.items()):
        lines.append(f"  {fixture} ({storage}):")
        for r in sorted(group, key=lambda x: x["runtime"]):
            cv = r["stddev"] / r["mean"] if r["mean"] > 0 else 0
            tail_ratio = r["p99"] / r["p50"] if r["p50"] > 0 else 0
            jitter = "stable" if tail_ratio < 3 else ("moderate" if tail_ratio < 6 else "spiky")
            lines.append(
                f"    {r['runtime']:<6} "
                f"CV={cv:.2f}  "
                f"P99/P50={tail_ratio:.1f}x  "
                f"→ {jitter}"
            )
    return "\n".join(lines)


def relative_performance(rows: list[dict]) -> str:
    """Show relative performance using Go as baseline."""
    lines = []
    by_fixture = defaultdict(list)
    for r in rows:
        by_fixture[(r["fixture"], r["storage"])].append(r)

    for (fixture, storage), group in sorted(by_fixture.items()):
        go_row = next((r for r in group if r["runtime"] == "go"), None)
        if not go_row:
            continue
        lines.append(f"  {fixture} ({storage}) — relative to Go:")
        for r in sorted(group, key=lambda x: -x["qps"]):
            qps_ratio = r["qps"] / go_row["qps"] if go_row["qps"] else 0
            p50_ratio = r["p50"] / go_row["p50"] if go_row["p50"] else 0
            lines.append(
                f"    {r['runtime']:<6} "
                f"QPS {qps_ratio:.2f}x  "
                f"P50 {p50_ratio:.2f}x"
            )
    return "\n".join(lines)


def verdict(rows: list[dict]) -> str:
    """One-line verdict per fixture for notifications."""
    lines = []
    by_fixture = defaultdict(list)
    for r in rows:
        by_fixture[(r["fixture"], r["storage"])].append(r)

    for (fixture, _storage), group in sorted(by_fixture.items()):
        group.sort(key=lambda x: -x["qps"])
        winner = group[0]
        if len(group) > 1:
            runner_up = group[1]
            lead = (winner["qps"] - runner_up["qps"]) / runner_up["qps"] * 100
            lines.append(
                f"{fixture}: {winner['runtime']} wins "
                f"({winner['qps']:.0f} QPS, +{lead:.0f}% over {runner_up['runtime']})"
            )
        else:
            lines.append(f"{fixture}: {winner['runtime']} ({winner['qps']:.0f} QPS)")
    return "\n".join(lines)


def format_text(meta: dict, rows: list[dict]) -> str:
    sections = []
    sections.append("=" * 60)
    sections.append("  Benchmark Analysis")
    if meta:
        sections.append(f"  {meta.get('date', '')}  {meta.get('machine', '')}")
        sections.append(f"  {meta.get('load', '')}")
    sections.append("=" * 60)
    sections.append("")

    sections.append("## Ranking")
    sections.append(ranking_table(rows))
    sections.append("")

    sections.append("## Relative Performance (vs Go)")
    sections.append(relative_performance(rows))
    sections.append("")

    sections.append("## Latency Stability")
    sections.append(latency_analysis(rows))
    sections.append("")

    sections.append("## Verdict")
    sections.append(verdict(rows))
    sections.append("")

    return "\n".join(sections)


def format_json(meta: dict, rows: list[dict]) -> str:
    by_fixture = defaultdict(list)
    for r in rows:
        by_fixture[(r["fixture"], r["storage"])].append(r)

    results = []
    for (fixture, storage), group in sorted(by_fixture.items()):
        group.sort(key=lambda x: -x["qps"])
        go_row = next((r for r in group if r["runtime"] == "go"), None)
        entry = {
            "fixture": fixture,
            "storage": storage,
            "winner": group[0]["runtime"],
            "runtimes": {},
        }
        for r in group:
            qps_vs_go = r["qps"] / go_row["qps"] if go_row and go_row["qps"] else None
            tail_ratio = r["p99"] / r["p50"] if r["p50"] > 0 else None
            entry["runtimes"][r["runtime"]] = {
                "qps": round(r["qps"], 2),
                "mean_ms": round(r["mean"] * 1000, 2),
                "p50_ms": round(r["p50"] * 1000, 2),
                "p90_ms": round(r["p90"] * 1000, 2),
                "p99_ms": round(r["p99"] * 1000, 2),
                "qps_vs_go": round(qps_vs_go, 3) if qps_vs_go else None,
                "tail_ratio": round(tail_ratio, 2) if tail_ratio else None,
            }
        results.append(entry)

    output = {"meta": meta, "results": results, "verdict": verdict(rows)}
    return json_mod.dumps(output, indent=2, ensure_ascii=False)


def main():
    parser = argparse.ArgumentParser(description="Analyze a cross-runtime benchmark report")
    parser.add_argument("report", help="Path to report.txt")
    parser.add_argument("--json", action="store_true", help="Output as JSON")
    parser.add_argument("--output", "-o", help="Output file (default: stdout)")
    args = parser.parse_args()

    text = Path(args.report).read_text()
    rows = parse_report(text)

    if not rows:
        print("No data rows found in report.", file=sys.stderr)
        sys.exit(1)

    meta = parse_meta(text)

    if args.json:
        output = format_json(meta, rows)
    else:
        output = format_text(meta, rows)

    if args.output:
        Path(args.output).write_text(output)
        print(f"Analysis written to {args.output}", file=sys.stderr)
    else:
        print(output)


if __name__ == "__main__":
    main()
