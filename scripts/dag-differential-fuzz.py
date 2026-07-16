"""DAG differential fuzzer: generates random pipeline configs, builds DAGs in all engines,
compares edge sets to ensure three-engine parity at the DAG construction level.

This complements differential-fuzz.py (which compares execution outputs) by checking
that the DAG builder itself produces identical dependency graphs.

Usage:
    python3 scripts/dag-differential-fuzz.py [--rounds N] [--seed S] [--engines go,java]
"""
from __future__ import annotations

import argparse
import json
import os
import random
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

FIELD_POOL = [f"f{i}" for i in range(8)]

_VALID_OP_TYPES = [
    "transform_copy", "transform_dispatch", "transform_size",
    "transform_by_lua", "filter_truncate", "recall_static", "reorder_sort",
    "observe_log",
]


def _random_fields(rng: random.Random, max_count: int) -> list[str]:
    count = rng.randint(0, max_count)
    return rng.sample(FIELD_POOL, min(count, len(FIELD_POOL)))


def gen_dag_config(rng: random.Random) -> tuple[dict, list[str]]:
    """Generate a random pipeline config suitable for DAG construction."""
    n = rng.randint(2, 8)
    pipeline: list[str] = []
    operators: dict[str, dict] = {}
    item_outputs: list[str] = []

    for i in range(n):
        name = f"op_{i}"
        pipeline.append(name)

        variant = rng.randint(0, 5)

        if variant == 0:
            out_fields = _random_fields(rng, 4) or ["f0"]
            operators[name] = {
                "type_name": "recall_static",
                "recall": True,
                "additive_writes_row_set": True,
                "items": [{"f0": j} for j in range(3)],
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": [], "item_output": out_fields,
                },
            }
            item_outputs = list(set(item_outputs + out_fields))

        elif variant == 1:
            operators[name] = {
                "type_name": "transform_copy",
                "direction": "common_to_item",
                "$metadata": {
                    "common_input": _random_fields(rng, 2),
                    "common_output": _random_fields(rng, 2),
                    "item_input": _random_fields(rng, 3),
                    "item_output": _random_fields(rng, 3),
                },
            }

        elif variant == 2:
            in_fields = _random_fields(rng, 3)
            operators[name] = {
                "type_name": "filter_truncate",
                "top_n": rng.randint(1, 20),
                "consumes_row_set": True,
                "mutates_row_set": True,
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": in_fields, "item_output": _random_fields(rng, 1),
                },
            }

        elif variant == 3:
            # Simulates a merge-like operator: consumes + mutates row set.
            # Uses transform_copy as type_name because DAG building only
            # looks at config flags, not the actual operator implementation.
            sources: list[str] = []
            if i > 0:
                src_count = rng.randint(0, min(i, 2))
                sources = rng.sample(pipeline[:i], src_count)
            operators[name] = {
                "type_name": "merge_dedup",
                "sources": sources,
                "consumes_row_set": True,
                "mutates_row_set": True,
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": [], "item_output": _random_fields(rng, 2),
                },
            }

        elif variant == 4:
            operators[name] = {
                "type_name": "reorder_sort",
                "order": "asc",
                "consumes_row_set": True,
                "mutates_row_set": True,
                "$metadata": {
                    "common_input": [], "common_output": [],
                    "item_input": _random_fields(rng, 3), "item_output": [],
                },
            }

        else:
            operators[name] = {
                "type_name": "observe_log",
                "$metadata": {
                    "common_input": _random_fields(rng, 2),
                    "common_output": [],
                    "item_input": _random_fields(rng, 2),
                    "item_output": [],
                },
            }

        if rng.random() < 0.15:
            op = operators[name]
            if not op.get("consumes_row_set"):
                op["consumes_row_set"] = True

    config = {
        "_PINEAPPLE_VERSION": "0.10.12",
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": {},
        },
        "pipeline_group": {"main": {"pipeline": pipeline}},
    }
    return config, pipeline


_EDGE_RE = re.compile(r'"([^"]+)"\s*->\s*"([^"]+)"')


def extract_edges(dot: str) -> set[tuple[str, str]]:
    return {(m.group(1), m.group(2)) for m in _EDGE_RE.finditer(dot)}


def run_go_dag(config_file: str) -> tuple[int, str, str]:
    go_dag = REPO_ROOT / "pine-go" / "cmd" / "pineapple-dag"
    result = subprocess.run(
        ["go", "run", str(go_dag), "-config", config_file, "-format", "dot"],
        capture_output=True, text=True, timeout=15, cwd=str(REPO_ROOT / "pine-go"),
    )
    return result.returncode, result.stdout, result.stderr


def _resolve_java_cp() -> str:
    target = REPO_ROOT / "pine-java" / "target" / "classes"
    if not target.exists():
        raise FileNotFoundError(f"Java not built: {target}")
    result = subprocess.run(
        ["mvn", "dependency:build-classpath", "-B", "-q", "-Dmdep.outputFile=/dev/stdout"],
        capture_output=True, text=True, cwd=str(REPO_ROOT / "pine-java"), timeout=60,
    )
    deps = result.stdout.strip().split("\n")[-1]
    return f"{target}:{deps}"


def run_java_dag(config_file: str, classpath: str) -> tuple[int, str, str]:
    result = subprocess.run(
        ["java", "-cp", classpath, "page.liam.pine.RenderDAGCli",
         "-config", config_file, "-format", "dot"],
        capture_output=True, text=True, timeout=15,
    )
    return result.returncode, result.stdout, result.stderr


def main():
    parser = argparse.ArgumentParser(description="DAG differential fuzzer: three-engine DAG parity")
    parser.add_argument("--rounds", type=int, default=500)
    parser.add_argument("--seed", type=int, default=None)
    parser.add_argument("--engines", type=str, default="go,java")
    parser.add_argument("--verbose", "-v", action="store_true")
    parser.add_argument("--save-dir", type=str, default="")
    args = parser.parse_args()

    seed = args.seed if args.seed is not None else random.randint(0, 2**32 - 1)
    rng = random.Random(seed)
    engine_names = [e.strip() for e in args.engines.split(",")]

    print(f"DAG differential fuzz: {args.rounds} rounds, seed={seed}, engines={engine_names}")

    java_cp = ""
    if "java" in engine_names:
        try:
            java_cp = _resolve_java_cp()
        except (FileNotFoundError, subprocess.TimeoutExpired) as e:
            print(f"  WARNING: Java not available ({e}), skipping")
            engine_names.remove("java")

    if len(engine_names) < 2:
        print("ERROR: need at least 2 engines")
        sys.exit(1)

    save_dir = Path(args.save_dir) if args.save_dir else Path(tempfile.mkdtemp(prefix="dag-diff-"))

    runners = {
        "go": run_go_dag,
        "java": lambda f: run_java_dag(f, java_cp),
    }

    passed = 0
    failed = 0
    errors = 0
    start = time.time()

    with tempfile.TemporaryDirectory() as tmpdir:
        config_file = os.path.join(tmpdir, "config.json")

        for i in range(args.rounds):
            try:
                config, pipeline = gen_dag_config(rng)
                config_abs = os.path.abspath(config_file)
                with open(config_abs, "w") as f:
                    json.dump(config, f, ensure_ascii=False)

                results: dict[str, tuple[int, str]] = {}
                for name in engine_names:
                    rc, out, err = runners[name](config_abs)
                    results[name] = (rc, out)

                ref = engine_names[0]
                ref_rc, ref_dot = results[ref]

                if ref_rc != 0:
                    ok = all(rc != 0 for rc, _ in results.values())
                    if ok:
                        passed += 1
                    else:
                        failed += 1
                        d = save_dir / f"diverge_{i:06d}"
                        d.mkdir(parents=True, exist_ok=True)
                        (d / "config.json").write_text(json.dumps(config, indent=2))
                        for n, (rc, dot) in results.items():
                            (d / f"{n}_rc{rc}.dot").write_text(dot)
                        print(f"\n  [{i+1}] DIVERGENCE (exit codes): {d}")
                    continue

                ref_edges = extract_edges(ref_dot)
                diverged = False
                for name in engine_names[1:]:
                    e_rc, e_dot = results[name]
                    if e_rc != 0:
                        diverged = True
                        break
                    e_edges = extract_edges(e_dot)
                    if ref_edges != e_edges:
                        diverged = True
                        only_ref = ref_edges - e_edges
                        only_e = e_edges - ref_edges
                        d = save_dir / f"diverge_{i:06d}"
                        d.mkdir(parents=True, exist_ok=True)
                        (d / "config.json").write_text(json.dumps(config, indent=2))
                        for n, (rc, dot) in results.items():
                            (d / f"{n}.dot").write_text(dot)
                        (d / "diff.txt").write_text(
                            f"only in {ref}: {only_ref}\nonly in {name}: {only_e}\n"
                        )
                        print(f"\n  [{i+1}] DAG DIVERGENCE ({ref} vs {name}): {d}")
                        if args.verbose:
                            print(f"       only in {ref}: {only_ref}")
                            print(f"       only in {name}: {only_e}")
                        break

                if diverged:
                    failed += 1
                else:
                    passed += 1

            except subprocess.TimeoutExpired:
                errors += 1
            except Exception as e:
                errors += 1
                if args.verbose:
                    print(f"\n  [{i+1}] error: {e}")

            elapsed = time.time() - start
            rate = (i + 1) / elapsed if elapsed > 0 else 0
            pct = (i + 1) * 100 // args.rounds
            sys.stderr.write(
                f"\r  {pct:3d}% ({i+1}/{args.rounds})"
                f" pass={passed} fail={failed}"
                f" err={errors} [{rate:.1f} rnd/s]"
            )
            sys.stderr.flush()

    sys.stderr.write("\n")
    elapsed = time.time() - start
    print(f"\n{'='*60}")
    print(f"DAG differential fuzz: {args.rounds} rounds, seed={seed}, {elapsed:.1f}s")
    print(f"  PASS: {passed}  FAIL: {failed}  ERROR: {errors}")
    if failed > 0:
        print(f"  Divergences: {save_dir}")
    print(f"{'='*60}")
    sys.exit(1 if failed > 0 else 0)


if __name__ == "__main__":
    main()
