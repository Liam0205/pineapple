#!/usr/bin/env python3
"""Op-attribution experiment: drop each op of large_5000 once and measure
single-binary CLI wall-clock of pine-cpp/pine-go, isolating the per-op
contribution to the C++ slowdown vs Go in large_5000.

Output: a markdown table to stdout."""

import json
import subprocess
import tempfile
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
FIXTURES = REPO_ROOT / "fixtures" / "benchmarks"
GO_BIN = REPO_ROOT / "pine-go" / "bin-bench" / "pineapple-run"
CPP_BIN = REPO_ROOT / "pine-cpp" / "build" / "pineapple-run"

ITERATIONS = 5
WARMUP = 1


def time_runs(binary, cfg, req, n_iter, warmup):
    samples = []
    for i in range(warmup + n_iter):
        t0 = time.perf_counter()
        subprocess.run(
            [str(binary), "-config", str(cfg), "-request", str(req)],
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=True,
        )
        dt = time.perf_counter() - t0
        if i >= warmup:
            samples.append(dt)
    samples.sort()
    return {
        "p50": samples[len(samples) // 2],
        "min": samples[0],
        "mean": sum(samples) / len(samples),
    }


def variants(base_ops):
    """yield (label, ops_after_drop)"""
    yield ("baseline", base_ops)
    for drop in [
        "copy",
        "lua_transform",
        "filter",
        "truncate",
        "normalize",
        "dispatch",
        "merge",
        "sort",
    ]:
        if drop not in base_ops["pipeline_map"]["stage1"]["pipeline"]:
            continue
        new_ops = json.loads(json.dumps(base_ops))
        new_ops["operators"].pop(drop, None)
        new_ops["pipeline_map"]["stage1"]["pipeline"] = [
            x
            for x in new_ops["pipeline_map"]["stage1"]["pipeline"]
            if x != drop
        ]
        yield (f"drop_{drop}", new_ops)


def main():
    base = json.load((FIXTURES / "large_5000_config.json").open())
    req = json.load((FIXTURES / "large_5000_request.json").open())
    base_pipe = base["pipeline_config"]

    work = Path(tempfile.mkdtemp(prefix="bench_attrib_"))
    print(f"# large_5000 op-attribution (iter={ITERATIONS}, warmup={WARMUP})")
    print(f"# tmp dir: {work}")
    print()
    print(
        f"{'variant':<20} {'go p50(s)':>11} {'cpp p50(s)':>12} "
        f"{'cpp/go':>8} {'go min':>8} {'cpp min':>8}"
    )
    print("-" * 75)

    req_path = work / "req.json"
    json.dump(req, req_path.open("w"))

    for label, pipe in variants(base_pipe):
        cfg = json.loads(json.dumps(base))
        cfg["pipeline_config"] = pipe
        cfg_path = work / f"cfg_{label}.json"
        json.dump(cfg, cfg_path.open("w"))

        try:
            go_t = time_runs(GO_BIN, cfg_path, req_path, ITERATIONS, WARMUP)
            cpp_t = time_runs(CPP_BIN, cfg_path, req_path, ITERATIONS, WARMUP)
        except subprocess.CalledProcessError as e:
            print(f"{label:<20}  FAILED: {e}")
            continue

        ratio = cpp_t["p50"] / go_t["p50"]
        print(
            f"{label:<20} {go_t['p50']:11.4f} {cpp_t['p50']:12.4f} "
            f"{ratio:8.2f} {go_t['min']:8.4f} {cpp_t['min']:8.4f}"
        )


if __name__ == "__main__":
    main()
