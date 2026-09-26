#!/usr/bin/env python3
"""Cross-engine benchmark runner — a unified performance comparison over each engine's HTTP server.

Usage:
    python3 scripts/cross-engine-bench.py [options]

Options:
    --iterations N          Sequential requests per fixture (default 200)
    --concurrency C         Concurrency levels (default "1,4,16,64")
    --fixtures-dir PATH     Benchmark fixture directory (default fixtures/benchmarks)
    --engines ENGINES       Engines to test (default "go,java")
    --output PATH           Result JSON path (default bench-results.json)
    --skip-build            Skip building the engines
    --warmup N              Warm-up requests (default 20)
    --tiers TIERS           Only test these tiers (e.g. "small,medium")

Workflow:
    1. Build the Go binary and the Java jar
    2. Start each engine's HTTP server in turn
    3. Send sequential/concurrent requests for every fixture and measure latency
    4. Print Markdown comparison tables + detailed JSON results
"""

from __future__ import annotations

import argparse
import json
import os
import socket
import statistics
import subprocess
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from contextlib import contextmanager
from dataclasses import dataclass, field
from pathlib import Path
from typing import Generator

REPO_ROOT = Path(__file__).resolve().parent.parent
FIXTURES_DIR = REPO_ROOT / "fixtures" / "benchmarks"

# Port assignment
PORTS = {"go": 9001, "java": 9002}


# ─── Data structures ──────────────────────────────────────────────────────────


@dataclass
class LatencyResult:
    """Latency stats for one engine on one fixture."""

    engine: str
    fixture: str
    iterations: int
    min_ms: float = 0.0
    median_ms: float = 0.0
    mean_ms: float = 0.0
    p95_ms: float = 0.0
    p99_ms: float = 0.0
    max_ms: float = 0.0
    error: str | None = None


@dataclass
class ThroughputResult:
    """Throughput stats for one engine on one fixture."""

    engine: str
    fixture: str
    concurrency: int
    rps: float = 0.0
    avg_ms: float = 0.0
    errors: int = 0
    total_requests: int = 0
    error: str | None = None


@dataclass
class BenchResults:
    """Full benchmark results."""

    timestamp: str = ""
    latency: list[LatencyResult] = field(default_factory=list)
    throughput: list[ThroughputResult] = field(default_factory=list)


# ─── Engine management ────────────────────────────────────────────────────────


def build_engines(engines: list[str], skip_build: bool = False):
    """Build the engines."""
    if skip_build:
        print("[build] 已跳过编译步骤")
        return

    bin_dir = REPO_ROOT / "bin"
    bin_dir.mkdir(exist_ok=True)

    if "go" in engines:
        print("[build] 编译 Go 引擎...")
        subprocess.run(
            ["go", "build", "-o", str(bin_dir / "pineapple-server"), "./cmd/pineapple-server/"],
            cwd=REPO_ROOT / "pine-go",
            check=True,
            capture_output=True,
        )
        print("[build] Go 编译完成")

    if "java" in engines:
        print("[build] 编译 Java 引擎...")
        subprocess.run(
            ["mvn", "package", "-q", "-DskipTests", "-Dmaven.javadoc.skip=true"],
            cwd=REPO_ROOT / "pine-java",
            check=True,
            capture_output=True,
        )
        print("[build] Java 编译完成")


def _wait_for_port(port: int, timeout: float = 30.0) -> bool:
    """Wait until the port accepts connections."""
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=1):
                return True
        except (ConnectionRefusedError, socket.timeout, OSError):
            time.sleep(0.1)
    return False


def _health_check(port: int) -> bool:
    """Check the /health endpoint."""
    try:
        req = urllib.request.Request(f"http://127.0.0.1:{port}/health")
        with urllib.request.urlopen(req, timeout=5) as resp:
            return resp.status == 200
    except Exception:
        return False


@contextmanager
def start_engine(
    engine: str, config_path: str, port: int
) -> Generator[subprocess.Popen | None, None, None]:
    """Start an engine server and yield its process handle; the context manager cleans it up."""
    proc: subprocess.Popen | None = None
    env = os.environ.copy()

    try:
        if engine == "go":
            binary = REPO_ROOT / "bin" / "pineapple-server"
            if not binary.exists():
                print(f"  [warn] Go binary 不存在: {binary}，尝试 go run")
                proc = subprocess.Popen(
                    [
                        "go",
                        "run",
                        "./cmd/pineapple-server/",
                        "-config",
                        config_path,
                        "-addr",
                        f":{port}",
                    ],
                    cwd=REPO_ROOT / "pine-go",
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    env=env,
                )
            else:
                proc = subprocess.Popen(
                    [str(binary), "-config", config_path, "-addr", f":{port}"],
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                    env=env,
                )

        elif engine == "java":
            jar_path = REPO_ROOT / "pine-java" / "target" / "pine-0.7.0.jar"
            # Resolve the classpath
            cp_result = subprocess.run(
                [
                    "mvn",
                    "dependency:build-classpath",
                    "-q",
                    "-DincludeScope=runtime",
                    "-Dmdep.outputFile=/dev/stdout",
                ],
                cwd=REPO_ROOT / "pine-java",
                capture_output=True,
                text=True,
            )
            dep_cp = cp_result.stdout.strip()
            classpath = f"{jar_path}:{dep_cp}" if dep_cp else str(jar_path)

            proc = subprocess.Popen(
                [
                    "java",
                    f"-Dpine.config={config_path}",
                    f"-Dpine.port={port}",
                    "-cp",
                    classpath,
                    "page.liam.pine.PineServer",
                ],
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                env=env,
            )

        if proc is None:
            yield None
            return

        # Wait for the server to be ready
        if not _wait_for_port(port, timeout=30.0):
            print(f"  [error] {engine} server 未能在 30s 内启动 (port {port})")
            proc.kill()
            yield None
            return

        # Extra health check
        if not _health_check(port):
            print(f"  [warn] {engine} server 端口开放但 /health 返回异常")

        yield proc

    finally:
        if proc is not None and proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()


# ─── Sending requests ─────────────────────────────────────────────────────────


def send_request(port: int, request_body: bytes) -> tuple[float, bool]:
    """Send one /execute request and return (latency in ms, success)."""
    url = f"http://127.0.0.1:{port}/execute"
    req = urllib.request.Request(
        url,
        data=request_body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            resp.read()  # drain the response body
            elapsed = (time.perf_counter() - start) * 1000.0
            return elapsed, resp.status == 200
    except (urllib.error.HTTPError, urllib.error.URLError, Exception):
        elapsed = (time.perf_counter() - start) * 1000.0
        return elapsed, False


# ─── Benchmark logic ──────────────────────────────────────────────────────────


def run_latency_bench(
    port: int,
    engine: str,
    fixture_name: str,
    request_body: bytes,
    iterations: int,
    warmup: int,
) -> LatencyResult:
    """Run the sequential latency benchmark for one engine on one fixture."""
    # Warm up
    for _ in range(warmup):
        send_request(port, request_body)

    # Measure
    latencies = []
    errors = 0
    for _ in range(iterations):
        ms, ok = send_request(port, request_body)
        if ok:
            latencies.append(ms)
        else:
            errors += 1

    if not latencies:
        return LatencyResult(
            engine=engine,
            fixture=fixture_name,
            iterations=iterations,
            error=f"all {iterations} requests failed",
        )

    latencies.sort()
    n = len(latencies)

    return LatencyResult(
        engine=engine,
        fixture=fixture_name,
        iterations=iterations,
        min_ms=latencies[0],
        median_ms=latencies[n // 2],
        mean_ms=statistics.mean(latencies),
        p95_ms=latencies[int(n * 0.95)],
        p99_ms=latencies[int(n * 0.99)],
        max_ms=latencies[-1],
        error=f"{errors} errors" if errors > 0 else None,
    )


def run_throughput_bench(
    port: int,
    engine: str,
    fixture_name: str,
    request_body: bytes,
    concurrency: int,
    duration_seconds: float = 5.0,
) -> ThroughputResult:
    """Run the concurrent throughput benchmark for one engine on one fixture."""
    total = 0
    errors = 0
    latencies: list[float] = []
    stop_time = time.perf_counter() + duration_seconds

    def worker():
        nonlocal total, errors
        local_total = 0
        local_errors = 0
        local_latencies: list[float] = []
        while time.perf_counter() < stop_time:
            ms, ok = send_request(port, request_body)
            local_total += 1
            local_latencies.append(ms)
            if not ok:
                local_errors += 1
        return local_total, local_errors, local_latencies

    with ThreadPoolExecutor(max_workers=concurrency) as executor:
        futures = [executor.submit(worker) for _ in range(concurrency)]
        for f in as_completed(futures):
            t, e, lats = f.result()
            total += t
            errors += e
            latencies.extend(lats)

    elapsed = duration_seconds
    rps = total / elapsed if elapsed > 0 else 0
    avg_ms = statistics.mean(latencies) if latencies else 0

    return ThroughputResult(
        engine=engine,
        fixture=fixture_name,
        concurrency=concurrency,
        rps=rps,
        avg_ms=avg_ms,
        errors=errors,
        total_requests=total,
    )


# ─── Fixture loading ──────────────────────────────────────────────────────────


def discover_fixtures(
    fixtures_dir: Path, tiers: list[str] | None = None
) -> list[tuple[str, Path, Path]]:
    """Discover every benchmark fixture and return a list of (name, config_path, request_path)."""
    fixtures = []
    for config_path in sorted(fixtures_dir.glob("*_config.json")):
        name = config_path.stem.replace("_config", "")
        request_path = fixtures_dir / f"{name}_request.json"
        if not request_path.exists():
            continue
        # Filter by tier
        if tiers:
            tier = name.split("_")[0]
            if tier not in tiers:
                continue
        fixtures.append((name, config_path, request_path))
    return fixtures


# ─── Output formatting ────────────────────────────────────────────────────────


def print_latency_table(results: list[LatencyResult], engines: list[str]):
    """Print the latency comparison as a Markdown table."""
    # Group by fixture
    fixtures = sorted(set(r.fixture for r in results))

    print("\n## Latency Comparison (sequential requests)\n")
    header = (
        "| Fixture | "
        + " | ".join(f"{e} median(ms)" for e in engines)
        + " | "
        + " | ".join(f"{e} p95(ms)" for e in engines)
        + " |"
    )
    sep = "|" + "---|" * (1 + len(engines) * 2)
    print(header)
    print(sep)

    for fixture in fixtures:
        row = f"| {fixture} "
        for e in engines:
            r = next((x for x in results if x.fixture == fixture and x.engine == e), None)
            if r and r.error is None:
                row += f"| {r.median_ms:.2f} "
            elif r:
                row += "| ERR "
            else:
                row += "| - "
        for e in engines:
            r = next((x for x in results if x.fixture == fixture and x.engine == e), None)
            if r and r.error is None:
                row += f"| {r.p95_ms:.2f} "
            elif r:
                row += "| ERR "
            else:
                row += "| - "
        row += "|"
        print(row)


def print_throughput_table(results: list[ThroughputResult], engines: list[str]):
    """Print the throughput comparison as a Markdown table."""
    fixtures = sorted(set(r.fixture for r in results))
    concurrencies = sorted(set(r.concurrency for r in results))

    print("\n## Throughput Comparison (RPS)\n")

    for conc in concurrencies:
        print(f"\n### Concurrency = {conc}\n")
        header = (
            "| Fixture | "
            + " | ".join(f"{e} RPS" for e in engines)
            + " | "
            + " | ".join(f"{e} avg(ms)" for e in engines)
            + " |"
        )
        sep = "|" + "---|" * (1 + len(engines) * 2)
        print(header)
        print(sep)

        for fixture in fixtures:
            row = f"| {fixture} "
            for e in engines:
                r = next(
                    (
                        x
                        for x in results
                        if x.fixture == fixture and x.engine == e and x.concurrency == conc
                    ),
                    None,
                )
                if r and r.error is None:
                    row += f"| {r.rps:.1f} "
                elif r:
                    row += "| ERR "
                else:
                    row += "| - "
            for e in engines:
                r = next(
                    (
                        x
                        for x in results
                        if x.fixture == fixture and x.engine == e and x.concurrency == conc
                    ),
                    None,
                )
                if r and r.error is None:
                    row += f"| {r.avg_ms:.2f} "
                elif r:
                    row += "| ERR "
                else:
                    row += "| - "
            row += "|"
            print(row)


def save_results(results: BenchResults, output_path: Path):
    """Save the results as JSON."""
    data = {
        "timestamp": results.timestamp,
        "latency": [
            {
                "engine": r.engine,
                "fixture": r.fixture,
                "iterations": r.iterations,
                "min_ms": r.min_ms,
                "median_ms": r.median_ms,
                "mean_ms": r.mean_ms,
                "p95_ms": r.p95_ms,
                "p99_ms": r.p99_ms,
                "max_ms": r.max_ms,
                "error": r.error,
            }
            for r in results.latency
        ],
        "throughput": [
            {
                "engine": r.engine,
                "fixture": r.fixture,
                "concurrency": r.concurrency,
                "rps": r.rps,
                "avg_ms": r.avg_ms,
                "errors": r.errors,
                "total_requests": r.total_requests,
                "error": r.error,
            }
            for r in results.throughput
        ],
    }
    with open(output_path, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False)
        f.write("\n")
    print(f"\n结果已保存到 {output_path}")


# ─── Main ─────────────────────────────────────────────────────────────────────


def main():
    parser = argparse.ArgumentParser(description="跨引擎 Benchmark Runner")
    parser.add_argument("--iterations", type=int, default=200, help="延迟测试迭代次数 (默认 200)")
    parser.add_argument("--concurrency", default="1,4,16,64", help="并发等级列表 (逗号分隔)")
    parser.add_argument("--fixtures-dir", type=Path, default=FIXTURES_DIR, help="fixture 目录")
    parser.add_argument("--engines", default="go,java", help="引擎列表 (逗号分隔)")
    parser.add_argument(
        "--output", type=Path, default=REPO_ROOT / "bench-results.json", help="结果输出路径"
    )
    parser.add_argument("--skip-build", action="store_true", help="跳过编译步骤")
    parser.add_argument("--warmup", type=int, default=20, help="预热请求数 (默认 20)")
    parser.add_argument("--tiers", default=None, help="仅测试指定层级 (如 small,medium)")
    parser.add_argument(
        "--throughput-duration", type=float, default=5.0, help="每个并发等级测试时长秒数 (默认 5)"
    )
    parser.add_argument("--latency-only", action="store_true", help="仅执行延迟测试")
    parser.add_argument("--throughput-only", action="store_true", help="仅执行吞吐量测试")
    args = parser.parse_args()

    engines = [e.strip() for e in args.engines.split(",")]
    concurrencies = [int(c.strip()) for c in args.concurrency.split(",")]
    tiers = [t.strip() for t in args.tiers.split(",")] if args.tiers else None

    print("=" * 60)
    print("Pineapple Cross-Engine Benchmark")
    print("=" * 60)
    print(f"  引擎: {engines}")
    print(f"  迭代: {args.iterations}")
    print(f"  并发: {concurrencies}")
    print(f"  预热: {args.warmup}")
    print()

    # Generate the fixtures if they do not exist
    if not args.fixtures_dir.exists() or not list(args.fixtures_dir.glob("*_config.json")):
        print("[fixtures] 生成 benchmark fixtures...")
        subprocess.run(
            [sys.executable, str(REPO_ROOT / "scripts" / "bench-generate-fixtures.py")],
            check=True,
        )

    # Discover the fixtures
    fixtures = discover_fixtures(args.fixtures_dir, tiers)
    if not fixtures:
        print("[error] 未找到任何 benchmark fixture")
        sys.exit(1)
    print(f"[fixtures] 发现 {len(fixtures)} 个 fixture")

    # Build the engines
    build_engines(engines, skip_build=args.skip_build)

    results = BenchResults(timestamp=time.strftime("%Y-%m-%dT%H:%M:%S"))

    # ─── Test each engine ───────────────────────────────────────────────
    for engine in engines:
        port = PORTS[engine]
        print(f"\n{'─' * 40}")
        print(f"[{engine}] 开始 benchmark (port {port})")
        print(f"{'─' * 40}")

        for fixture_name, config_path, request_path in fixtures:
            print(f"\n  [{engine}] fixture: {fixture_name}")

            request_body = request_path.read_bytes()

            with start_engine(engine, str(config_path), port) as proc:
                if proc is None:
                    print(f"    [skip] {engine} server 启动失败")
                    results.latency.append(
                        LatencyResult(
                            engine=engine,
                            fixture=fixture_name,
                            iterations=0,
                            error="server start failed",
                        )
                    )
                    continue

                # Latency test
                if not args.throughput_only:
                    print(f"    延迟测试 ({args.iterations} iterations, {args.warmup} warmup)...")
                    lat = run_latency_bench(
                        port,
                        engine,
                        fixture_name,
                        request_body,
                        args.iterations,
                        args.warmup,
                    )
                    results.latency.append(lat)
                    if lat.error:
                        print(f"    结果: ERROR - {lat.error}")
                    else:
                        print(
                            f"    结果: median={lat.median_ms:.2f}ms p95={lat.p95_ms:.2f}ms "
                            f"p99={lat.p99_ms:.2f}ms"
                        )

                # Throughput test
                if not args.latency_only:
                    for conc in concurrencies:
                        print(
                            f"    吞吐量测试 (concurrency={conc}, "
                            f"duration={args.throughput_duration}s)..."
                        )
                        tp = run_throughput_bench(
                            port,
                            engine,
                            fixture_name,
                            request_body,
                            conc,
                            args.throughput_duration,
                        )
                        results.throughput.append(tp)
                        if tp.error:
                            print(f"      结果: ERROR - {tp.error}")
                        else:
                            print(
                                f"      结果: RPS={tp.rps:.1f} avg={tp.avg_ms:.2f}ms "
                                f"errors={tp.errors}"
                            )

    # ─── Print the results ──────────────────────────────────────────────
    print("\n" + "=" * 60)
    print("RESULTS")
    print("=" * 60)

    if results.latency:
        print_latency_table(results.latency, engines)

    if results.throughput:
        print_throughput_table(results.throughput, engines)

    save_results(results, args.output)

    return 0


if __name__ == "__main__":
    sys.exit(main())
