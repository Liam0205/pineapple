#!/usr/bin/env python3
"""Analyze a single cross-runtime benchmark report and produce a summary."""
import argparse
import sys
from collections import defaultdict
from pathlib import Path


def parse_report(text: str) -> list[dict]:
    rows = []
    for line in text.splitlines():
        parts = line.split()
        if len(parts) != 11:
            continue
        try:
            rows.append({
                "runtime": parts[0],
                "nodes": int(parts[1]),
                "storage": parts[2],
                "par": int(parts[3]),
                "op": parts[4],
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


def group_by(rows: list[dict], key: str) -> dict[str, list[dict]]:
    groups: dict[str, list[dict]] = defaultdict(list)
    for r in rows:
        groups[r[key]].append(r)
    return groups


def avg(values: list[float]) -> float:
    return sum(values) / len(values) if values else 0.0


def section_runtime_ranking(rows: list[dict]) -> str:
    lines = ["## Runtime Ranking by Op Type + DAG Size", ""]
    by_op = group_by(rows, "op")
    for op in sorted(by_op):
        by_nodes = group_by(by_op[op], "nodes")
        for nodes in sorted(by_nodes):
            by_rt = group_by(by_nodes[nodes], "runtime")
            rt_qps = []
            for rt, rt_rows in by_rt.items():
                rt_qps.append((rt, avg([r["qps"] for r in rt_rows])))
            rt_qps.sort(key=lambda x: -x[1])
            ranking = " > ".join(f"{rt}({qps:.0f})" for rt, qps in rt_qps)
            lines.append(f"  {op:>5} nodes={nodes:<4}  {ranking}")
    lines.append("")
    return "\n".join(lines)


def section_parallelism_effect(rows: list[dict]) -> str:
    lines = ["## Parallelism Effect (QPS at par=1 vs max par)", ""]
    by_op = group_by(rows, "op")
    for op in sorted(by_op):
        by_nodes = group_by(by_op[op], "nodes")
        for nodes in sorted(by_nodes):
            by_rt = group_by(by_nodes[nodes], "runtime")
            for rt in sorted(by_rt):
                rt_rows = by_rt[rt]
                by_par = group_by(rt_rows, "par")
                pars = sorted(by_par.keys())
                if len(pars) < 2:
                    continue
                qps_min_par = avg([r["qps"] for r in by_par[pars[0]]])
                qps_max_par = avg([r["qps"] for r in by_par[pars[-1]]])
                if qps_min_par == 0:
                    continue
                speedup = qps_max_par / qps_min_par
                marker = " <<<" if speedup > 1.3 else (" !!!" if speedup < 0.8 else "")
                lines.append(
                    f"  {rt:<6} {op:>5} nodes={nodes:<4} "
                    f"par={pars[0]}→{pars[-1]}  "
                    f"{qps_min_par:>8.1f} → {qps_max_par:>8.1f} QPS  "
                    f"({speedup:.2f}x){marker}"
                )
    lines.append("")
    return "\n".join(lines)


def section_storage_effect(rows: list[dict]) -> str:
    lines = ["## Storage Mode Effect (row vs column avg QPS)", ""]
    by_storage = group_by(rows, "storage")
    storages = sorted(by_storage.keys())
    if len(storages) < 2:
        lines.append("  Only one storage mode in data.")
        lines.append("")
        return "\n".join(lines)

    by_rt = group_by(rows, "runtime")
    for rt in sorted(by_rt):
        for op in sorted(set(r["op"] for r in rows)):
            row_qps = [r["qps"] for r in by_rt[rt] if r["storage"] == "row" and r["op"] == op]
            col_qps = [r["qps"] for r in by_rt[rt] if r["storage"] == "column" and r["op"] == op]
            if not row_qps or not col_qps:
                continue
            r_avg = avg(row_qps)
            c_avg = avg(col_qps)
            diff_pct = (c_avg - r_avg) / r_avg * 100 if r_avg else 0
            marker = " *" if abs(diff_pct) > 5 else ""
            lines.append(
                f"  {rt:<6} {op:>5}  row={r_avg:>8.1f}  col={c_avg:>8.1f}  "
                f"delta={diff_pct:+.1f}%{marker}"
            )
    lines.append("")
    return "\n".join(lines)


def section_latency_outliers(rows: list[dict]) -> str:
    lines = ["## Latency Outliers (high stddev/mean ratio or P99/P50 spread)", ""]
    outliers = []
    for r in rows:
        cv = r["stddev"] / r["mean"] if r["mean"] > 0 else 0
        tail_ratio = r["p99"] / r["p50"] if r["p50"] > 0 else 0
        if cv > 0.3 or tail_ratio > 2.0:
            outliers.append((cv, tail_ratio, r))

    outliers.sort(key=lambda x: -x[0])
    if not outliers:
        lines.append("  No significant outliers detected.")
    else:
        lines.append(f"  {'Runtime':<6} {'Nodes':>5} {'Stor':>6} {'Par':>3} {'Op':>5}"
                     f"  {'CV':>6}  {'P99/P50':>7}  {'Mean':>8}  {'Stddev':>8}")
        lines.append(f"  {'------':<6} {'-----':>5} {'------':>6} {'---':>3} {'-----':>5}"
                     f"  {'------':>6}  {'-------':>7}  {'--------':>8}  {'--------':>8}")
        for cv, tail, r in outliers[:15]:
            lines.append(
                f"  {r['runtime']:<6} {r['nodes']:>5} {r['storage']:>6} {r['par']:>3} {r['op']:>5}"
                f"  {cv:>6.2f}  {tail:>7.2f}  {r['mean']:>8.4f}  {r['stddev']:>8.4f}"
            )
    lines.append("")
    return "\n".join(lines)


def section_summary(rows: list[dict]) -> str:
    lines = ["## Key Takeaways", ""]
    runtimes = sorted(set(r["runtime"] for r in rows))
    ops = sorted(set(r["op"] for r in rows))

    for op in ops:
        wins: dict[str, int] = defaultdict(int)
        by_config = defaultdict(list)
        for r in rows:
            if r["op"] != op:
                continue
            key = (r["nodes"], r["storage"], r["par"])
            by_config[key].append(r)
        for key, config_rows in by_config.items():
            best = max(config_rows, key=lambda x: x["qps"])
            wins[best["runtime"]] += 1
        total = sum(wins.values())
        win_str = ", ".join(f"{rt}={wins.get(rt, 0)}/{total}" for rt in runtimes)
        lines.append(f"  {op}: wins by config → {win_str}")

    lines.append("")
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description="Analyze a benchmark report")
    parser.add_argument("report", help="Path to report.txt")
    parser.add_argument("--output", "-o", help="Output file (default: stdout)")
    args = parser.parse_args()

    text = Path(args.report).read_text()
    rows = parse_report(text)

    if not rows:
        print("No data rows found in report.", file=sys.stderr)
        sys.exit(1)

    header_lines = [l for l in text.splitlines() if l.startswith("═") or l.startswith(" ")]
    meta = "\n".join(header_lines[:7])

    sections = [
        f"# Benchmark Analysis\n\n{meta}\n",
        section_runtime_ranking(rows),
        section_parallelism_effect(rows),
        section_storage_effect(rows),
        section_latency_outliers(rows),
        section_summary(rows),
    ]

    output = "\n".join(sections)
    if args.output:
        Path(args.output).write_text(output)
        print(f"Analysis written to {args.output}")
    else:
        print(output)


if __name__ == "__main__":
    main()
