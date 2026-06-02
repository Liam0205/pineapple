"""Base operator class for Apple DSL.

All generated operator classes inherit from BaseOp. The _apply method records
operator invocations for later compilation to JSON.
"""
from __future__ import annotations

import hashlib
import inspect
from dataclasses import dataclass, field
from typing import Any


def _lookup_markers(type_name: str) -> dict[str, bool]:
    """Look up row-set marker bools for ``type_name`` from the codegen table.

    Returns all-False if ``apple_generated.markers`` is unavailable (e.g.,
    codegen has not been run yet) or the operator is unknown. The Go side
    remains the source of truth — this table is a static mirror.
    """
    try:
        from apple_generated.markers import get_markers  # type: ignore
    except ImportError:
        return {
            "additive_writes_row_set": False,
            "consumes_row_set": False,
            "mutates_row_set": False,
        }
    return get_markers(type_name)


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
    strict_common: list[str] = field(default_factory=list)
    strict_item: list[str] = field(default_factory=list)
    # Engine-level flags
    recall: bool = False
    sources: list[str] | None = None
    skip: list[str] = field(default_factory=list)
    for_branch_control: bool = False
    consumes_row_set: bool = False
    additive_writes_row_set: bool = False
    mutates_row_set: bool = False
    data_parallel: int = 0
    debug: bool = False
    # Debug info
    code_info: str = ""
    subflow_path: str = ""
    # Explicit name (overrides auto-generated name)
    name: str = ""

    def unique_name(self) -> str:
        """Return explicit name if set, otherwise generate type_name_HASH6.

        Hash input excludes code_info (source file path + line number) since
        it is purely debugging metadata and should not affect the operator's
        identity. subflow_path is intentionally included because it reflects
        the operator's position in the DAG topology.
        """
        if self.name:
            return self.name
        semantic = (
            self.type_name,
            repr(self.params),
            tuple(self.common_input),
            tuple(self.common_output),
            tuple(self.item_input),
            tuple(self.item_output),
            repr(self.item_defaults),
            repr(self.common_defaults),
            tuple(self.strict_common),
            tuple(self.strict_item),
            self.recall,
            tuple(self.sources) if self.sources else (),
            tuple(self.skip) if self.skip else (),
            self.for_branch_control,
            self.consumes_row_set,
            self.additive_writes_row_set,
            self.mutates_row_set,
            self.data_parallel,
            self.debug,
            self.subflow_path,
        )
        h = hashlib.md5(repr(semantic).encode()).hexdigest()[:6].upper()
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
        consumes_row_set: bool = False,
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

        # Pull row-set markers from the codegen-generated table. The Go side
        # is the source of truth; this lookup mirrors AdditiveWritesRowSet /
        # ConsumesRowSet / MutatesRowSet interface assertions.
        markers = _lookup_markers(self._name)
        # Caller-supplied consumes_row_set (legacy API on BaseOp.__call__) wins
        # if explicitly True — preserves the hand-rolled override path while
        # defaulting to the marker-derived value otherwise.
        effective_consumes = consumes_row_set or markers["consumes_row_set"]

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
            consumes_row_set=effective_consumes,
            additive_writes_row_set=markers["additive_writes_row_set"],
            mutates_row_set=markers["mutates_row_set"],
            debug=debug,
            code_info=code_info,
            name=name,
        )

        self._flow._apply_skip_fields(call, self._flow._active_skip_fields())

        idx = len(self._flow._ops)
        self._flow._ops.append(call)
        self._flow._child_order.append(("op", idx))
        return self._flow
