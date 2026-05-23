"""Pluggable metrics abstraction mirroring pine-go/pkg/metrics.

Third-party projects implement Provider with their Prometheus / OTel /
StatsD backend; Pineapple itself only depends on the abstract base
classes here. The default NopProvider is used when callers do not
supply a custom provider, so the HTTP middleware can always wrap the
handler chain identically to pine-go.
"""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class MetricOpts:
    name: str
    help: str
    label_names: tuple[str, ...] = ()


@dataclass(frozen=True)
class HistogramOpts:
    name: str
    help: str
    buckets: tuple[float, ...]
    label_names: tuple[str, ...] = ()


class Counter:
    """Abstract counter. Implementations may be no-op or backend-backed."""

    def with_(self, *label_values: str) -> "Counter":
        return self

    def inc(self) -> None:
        pass


class Gauge:
    def with_(self, *label_values: str) -> "Gauge":
        return self

    def set(self, value: float) -> None:
        pass

    def add(self, delta: float) -> None:
        pass


class Histogram:
    def with_(self, *label_values: str) -> "Histogram":
        return self

    def observe(self, value: float) -> None:
        pass


class Provider:
    """Abstract Provider. Implementations construct backend-backed metric handles."""

    def new_counter(self, opts: MetricOpts) -> Counter:
        raise NotImplementedError

    def new_gauge(self, opts: MetricOpts) -> Gauge:
        raise NotImplementedError

    def new_histogram(self, opts: HistogramOpts) -> Histogram:
        raise NotImplementedError


class _NopCounter(Counter):
    pass


class _NopGauge(Gauge):
    pass


class _NopHistogram(Histogram):
    pass


class NopProvider(Provider):
    """Default no-op provider: discards all observations.

    Used when callers do not supply a custom Provider so the HTTP
    middleware chain stays identical to the Go/Java/C++ runtimes.
    """

    _instance: "NopProvider | None" = None
    _counter = _NopCounter()
    _gauge = _NopGauge()
    _histogram = _NopHistogram()

    def __new__(cls):
        if cls._instance is None:
            cls._instance = super().__new__(cls)
        return cls._instance

    def new_counter(self, opts: MetricOpts) -> Counter:
        return self._counter

    def new_gauge(self, opts: MetricOpts) -> Gauge:
        return self._gauge

    def new_histogram(self, opts: HistogramOpts) -> Histogram:
        return self._histogram


def Nop() -> NopProvider:  # noqa: N802 — name mirrors pine-go metrics.Nop()
    """Returns the singleton no-op Provider."""
    return NopProvider()


def duration_seconds(td_seconds: float) -> float:
    """Identity transform; mirrors pine-go metrics.DurationSeconds for API parity."""
    return float(td_seconds)
