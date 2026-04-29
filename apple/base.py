"""Base operator class for Apple DSL.

All generated operator classes inherit from BaseOp. The _apply method records
operator invocations for later compilation to JSON.
"""
from __future__ import annotations

import hashlib
import inspect
from dataclasses import dataclass, field
from typing import Any


@dataclass
class OpCall:
    """Record of a single operator invocation in a flow."""
    type_name: str
    params: dict[str, Any]
    common_input: list[str] = field(default_factory=list)
    common_output: list[str] = field(default_factory=list)
    item_input: list[str] = field(default_factory=list)
    item_output: list[str] = field(default_factory=list)
    item_defaults: dict[str, Any] | None = None
    common_defaults: dict[str, Any] | None = None
    # Engine-level flags
    recall: bool = False
    sources: list[str] | None = None
    skip: list[str] = field(default_factory=list)
    for_branch_control: bool = False
    row_dependency: bool = False
    data_parallel: int = 0
    debug: bool = False
    # Debug info
    code_info: str = ""
    subflow_path: str = ""
    # Explicit name (overrides auto-generated name)
    name: str = ""

    def unique_name(self) -> str:
        """Return explicit name if set, otherwise generate type_name_HASH6."""
        if self.name:
            return self.name
        h = hashlib.md5(repr(self).encode()).hexdigest()[:6].upper()
        return f"{self.type_name}_{h}"


class BaseOp:
    """Base class for all operator types in the Apple DSL.

    Subclasses define _name and _params_schema. The __call__ method delegates
    to _apply, which records the call into the owning flow.
    """
    _name: str = ""
    _params_schema: dict[str, Any] = {}

    def __init__(self, flow: Any):
        self._flow = flow

    def _apply(
        self,
        params: dict[str, Any],
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict[str, Any] | None = None,
        common_defaults: dict[str, Any] | None = None,
        recall: bool = False,
        sources: list[str] | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str = "",
    ) -> Any:
        # Capture caller location for $code_info
        code_info = ""
        try:
            frame = inspect.stack()[1]
            code_info = f"{frame.filename}:{frame.lineno} in {frame.function}(): .{self._name}(...)"
        except (IndexError, AttributeError):
            pass

        call = OpCall(
            type_name=self._name,
            params=params,
            common_input=common_input or [],
            common_output=common_output or [],
            item_input=item_input or [],
            item_output=item_output or [],
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            recall=recall,
            sources=sources,
            row_dependency=row_dependency,
            debug=debug,
            code_info=code_info,
            name=name,
        )

        self._flow._apply_skip_fields(call, self._flow._active_skip_fields())

        idx = len(self._flow._ops)
        self._flow._ops.append(call)
        self._flow._child_order.append(("op", idx))
        return self._flow
