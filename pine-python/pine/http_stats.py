"""In-process atomic accumulators for HTTP request observability.

Mirrors pine-go ``pkg/server.HttpStats``. The HTTP metrics middleware
writes both an external Provider (Counter/Histogram) and this
in-memory structure so /stats can expose request counts and duration
sums without requiring a Prometheus adapter.

Keys are byte-exact with the Go reference:
  requests_total:           "<METHOD> <path> <statusBucket>"
  request_duration_seconds: "<METHOD> <path>"
"""
from __future__ import annotations

import threading
from dataclasses import dataclass
from typing import Any


@dataclass
class _DurationBucket:
    count: int = 0
    sum_ns: int = 0


class HttpStats:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._requests: dict[str, int] = {}
        self._durations: dict[str, _DurationBucket] = {}

    def record_request(
        self, method: str, path: str, status_bucket: str, duration_ns: int
    ) -> None:
        req_key = f"{method} {path} {status_bucket}"
        dur_key = f"{method} {path}"
        with self._lock:
            self._requests[req_key] = self._requests.get(req_key, 0) + 1
            bucket = self._durations.setdefault(dur_key, _DurationBucket())
            bucket.count += 1
            bucket.sum_ns += duration_ns

    def snapshot(self) -> dict[str, Any]:
        """Returns a deterministic snapshot.

        Outer maps are sorted by key ascending so JSON encoding matches
        Go's encoding/json (which sorts string-keyed map keys). Inner
        duration buckets emit ``count`` then ``sum_ns``.
        """
        with self._lock:
            req_copy = dict(self._requests)
            dur_copy = {
                k: (v.count, v.sum_ns) for k, v in self._durations.items()
            }

        req_out = {k: req_copy[k] for k in sorted(req_copy)}
        dur_out = {
            k: {"count": dur_copy[k][0], "sum_ns": dur_copy[k][1]}
            for k in sorted(dur_copy)
        }
        return {
            "request_duration_seconds": dur_out,
            "requests_total": req_out,
        }
