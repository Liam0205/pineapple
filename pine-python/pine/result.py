"""Public data classes returned by Engine.execute()."""
from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass
class Warning:
    """Non-fatal warning emitted by an operator."""
    operator: str
    err: Exception


@dataclass
class OpTrace:
    """Execution trace for a single operator invocation."""
    name: str
    duration_ns: int
    skipped: bool
    input_snapshot: dict[str, Any] | None = None
    output_snapshot: dict[str, Any] | None = None


@dataclass
class Result:
    """Execution result returned by Engine.execute()."""
    common: dict[str, Any]
    items: list[dict[str, Any]]
    warnings: list[Warning] = field(default_factory=list)
    trace: list[OpTrace] = field(default_factory=list)
    error: Exception | None = None
