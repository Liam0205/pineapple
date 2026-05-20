"""Internal execution statistics tracking (thread-safe)."""
from __future__ import annotations

import threading
from typing import Any


class _OpStats:
    __slots__ = ("exec_count", "skip_count", "error_count",
                 "total_duration_ns", "max_duration_ns", "_lock")

    def __init__(self):
        self._lock = threading.Lock()
        self.exec_count: int = 0
        self.skip_count: int = 0
        self.error_count: int = 0
        self.total_duration_ns: int = 0
        self.max_duration_ns: int = 0

    def record_exec(self, duration_ns: int):
        with self._lock:
            self.exec_count += 1
            self.total_duration_ns += duration_ns
            if duration_ns > self.max_duration_ns:
                self.max_duration_ns = duration_ns

    def record_skip(self):
        with self._lock:
            self.skip_count += 1

    def record_error(self, duration_ns: int):
        with self._lock:
            self.error_count += 1
            self.total_duration_ns += duration_ns
            if duration_ns > self.max_duration_ns:
                self.max_duration_ns = duration_ns

    def snapshot(self) -> dict[str, Any]:
        with self._lock:
            exec_c = self.exec_count
            total = self.total_duration_ns
            return {
                "exec_count": exec_c,
                "skip_count": self.skip_count,
                "error_count": self.error_count,
                "total_duration_ns": total,
                "max_duration_ns": self.max_duration_ns,
                "avg_duration_ns": total // exec_c if exec_c > 0 else 0,
            }


class _Stats:
    def __init__(self):
        self._lock = threading.Lock()
        self._ops: dict[str, _OpStats] = {}
        self._run_count: int = 0
        self._peak_concurrency: int = 0

    def pre_init_operators(self, names: list[str]):
        """Pre-register all operator names so they appear in stats from startup."""
        with self._lock:
            for name in names:
                if name not in self._ops:
                    self._ops[name] = _OpStats()

    def _get_or_create(self, name: str) -> _OpStats:
        with self._lock:
            op = self._ops.get(name)
            if op is None:
                op = _OpStats()
                self._ops[name] = op
            return op

    def record_exec(self, name: str, duration_ns: int):
        self._get_or_create(name).record_exec(duration_ns)

    def record_skip(self, name: str):
        self._get_or_create(name).record_skip()

    def record_error(self, name: str, duration_ns: int):
        self._get_or_create(name).record_error(duration_ns)

    def record_run(self):
        with self._lock:
            self._run_count += 1

    def record_concurrency(self, n: int):
        with self._lock:
            if n > self._peak_concurrency:
                self._peak_concurrency = n

    def snapshot(self) -> dict[str, dict[str, Any]]:
        with self._lock:
            names = sorted(self._ops.keys())
        result: dict[str, dict[str, Any]] = {}
        for name in names:
            result[name] = self._get_or_create(name).snapshot()
        return result

    def scheduler_snapshot(self) -> dict[str, Any]:
        with self._lock:
            return {
                "run_count": self._run_count,
                "peak_concurrency": self._peak_concurrency,
            }
