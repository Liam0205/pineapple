"""Flow and SubFlow classes for Apple DSL pipeline declaration.

Usage:
    flow = Flow(
        name="example",
        common_input=["user_id", "user_age"],
        item_output=["item_score"],
    )
    flow.recall_static(
        item_output=["item_id", "item_score"],
        items=[{"item_id": "a", "item_score": 1.0}],
    )
    flow.filter_condition(
        item_input=["item_status"],
        field="item_status", value="offline",
    )
    json_config = flow.compile()
"""
from __future__ import annotations

import inspect
from typing import Any

from apple.base import OpCall
from apple.compiler import compile_flow, compile_to_json
from apple.control import (
    ControlBlock,
    ControlBranch,
    make_control_op,
)
from apple.resource import BaseResource, ResourceDecl


def _capture_code_info(type_name: str, stack_depth: int = 2) -> str:
    """Capture the caller's source location for $code_info.

    Walks up the call stack to find the first frame outside apple/ package,
    which is the user's DSL code.
    """
    try:
        frame = inspect.stack()[stack_depth]
        filename = frame.filename
        lineno = frame.lineno
        func_name = frame.function
        return f"{filename}:{lineno} in {func_name}(): .{type_name}(...)"
    except (IndexError, AttributeError):
        return ""


class _FlowBase:
    """Shared chaining logic for Flow and SubFlow."""

    def __init__(self, name: str):
        self._name = name
        self._ops: list[OpCall] = []
        self._sub_flows: list[SubFlow] = []
        self._child_order: list[tuple[str, int]] = []  # ("op", idx) or ("sf", idx)
        # Control flow state
        self._ctrl_counter = 0
        self._ctrl_stack: list[ControlBlock] = []
        self._parent_skips: list[str] = []

    def _active_skip_fields(self, include_current: bool = True) -> list[str]:
        """Return control fields that guard declarations at the current point."""
        stack = self._ctrl_stack if include_current else self._ctrl_stack[:-1]
        fields: list[str] = []
        for block in stack:
            if block.branches:
                fields.append(block.branches[-1].ctrl_field)
        return fields

    def _apply_skip_fields(self, call: OpCall, skip_fields: list[str]) -> None:
        """Attach control-flow skip dependencies to an operator call."""
        missing_inputs: list[str] = []
        for skip_field in skip_fields:
            if skip_field not in call.skip:
                call.skip.append(skip_field)
            if skip_field not in call.common_input:
                missing_inputs.append(skip_field)
        call.common_input = missing_inputs + call.common_input

    def __getattr__(self, name: str) -> Any:
        """Dynamic operator dispatch: flow.some_op(...) creates an OpCall."""
        if name.startswith("_"):
            raise AttributeError(name)

        def op_caller(**kwargs: Any) -> _FlowBase:
            return self._add_op(name, **kwargs)

        return op_caller

    def _add_op(self, type_name: str, **kwargs: Any) -> _FlowBase:
        """Record an operator call."""
        # Capture caller location for $code_info
        code_info = _capture_code_info(type_name)

        # Separate metadata kwargs from business params
        meta_keys = {
            "name", "common_input", "common_output", "item_input", "item_output",
            "item_defaults", "common_defaults", "sources", "debug",
        }
        meta = {}
        params = {}
        for k, v in kwargs.items():
            if k in meta_keys:
                meta[k] = v
            elif k == "recall":
                # Accept but ignore — recall is now type-driven from prefix
                pass
            elif k == "data_parallel":
                meta[k] = v
            else:
                params[k] = v

        # Recall is inferred from operator name prefix, not user-passed
        is_recall = type_name.startswith("recall_")

        call = OpCall(
            type_name=type_name,
            params=params,
            common_input=meta.get("common_input", []),
            common_output=meta.get("common_output", []),
            item_input=meta.get("item_input", []),
            item_output=meta.get("item_output", []),
            item_defaults=meta.get("item_defaults"),
            common_defaults=meta.get("common_defaults"),
            recall=is_recall,
            sources=meta.get("sources"),
            debug=meta.get("debug", False),
            data_parallel=meta.get("data_parallel", 0),
            code_info=code_info,
            name=meta.get("name", ""),
        )

        # Apply all active branch guards, including outer nested control blocks.
        self._apply_skip_fields(call, self._active_skip_fields())

        idx = len(self._ops)
        self._ops.append(call)
        self._child_order.append(("op", idx))
        return self

    def add_subflow(self, sf: SubFlow) -> _FlowBase:
        """Add a nested SubFlow, preserving declaration order with ops."""
        if "/" in sf._name:
            raise ValueError(
                f"SubFlow name must not contain '/': {sf._name!r}"
            )
        sf._parent_skips = self._active_skip_fields()
        idx = len(self._sub_flows)
        self._sub_flows.append(sf)
        self._child_order.append(("sf", idx))
        return self

    # --- Control flow ---

    def if_(self, condition: str) -> _FlowBase:
        """Start an if block."""
        parent_skips = self._active_skip_fields()
        self._ctrl_counter += 1
        block = ControlBlock(block_id=self._ctrl_counter)
        ctrl_field = f"_if_{block.block_id}"

        branch = ControlBranch(
            kind="if",
            condition=condition,
            ctrl_field=ctrl_field,
            ctrl_index=self._ctrl_counter,
        )
        block.branches.append(branch)
        self._ctrl_stack.append(block)

        # Emit the control operator
        ctrl_op = make_control_op(branch, [], condition)
        self._apply_skip_fields(ctrl_op, parent_skips)
        idx = len(self._ops)
        self._ops.append(ctrl_op)
        self._child_order.append(("op", idx))
        return self

    def elseif_(self, condition: str) -> _FlowBase:
        """Add an elseif branch."""
        if not self._ctrl_stack:
            raise ValueError("elseif_ without matching if_")
        block = self._ctrl_stack[-1]
        if block.closed:
            raise ValueError("elseif_ after end_if_")
        parent_skips = self._active_skip_fields(include_current=False)

        self._ctrl_counter += 1
        prior_fields = [b.ctrl_field for b in block.branches]
        ctrl_field = f"_elif_{self._ctrl_counter}"

        branch = ControlBranch(
            kind="elseif",
            condition=condition,
            ctrl_field=ctrl_field,
            ctrl_index=self._ctrl_counter,
        )
        block.branches.append(branch)

        ctrl_op = make_control_op(branch, prior_fields, condition)
        self._apply_skip_fields(ctrl_op, parent_skips)
        idx = len(self._ops)
        self._ops.append(ctrl_op)
        self._child_order.append(("op", idx))
        return self

    def else_(self) -> _FlowBase:
        """Add an else branch."""
        if not self._ctrl_stack:
            raise ValueError("else_ without matching if_")
        block = self._ctrl_stack[-1]
        if block.closed:
            raise ValueError("else_ after end_if_")
        parent_skips = self._active_skip_fields(include_current=False)

        self._ctrl_counter += 1
        prior_fields = [b.ctrl_field for b in block.branches]
        ctrl_field = f"_else_{self._ctrl_counter}"

        branch = ControlBranch(
            kind="else",
            condition=None,
            ctrl_field=ctrl_field,
            ctrl_index=self._ctrl_counter,
        )
        block.branches.append(branch)

        ctrl_op = make_control_op(branch, prior_fields, None)
        self._apply_skip_fields(ctrl_op, parent_skips)
        idx = len(self._ops)
        self._ops.append(ctrl_op)
        self._child_order.append(("op", idx))
        return self

    def end_if_(self) -> _FlowBase:
        """Close the current if block."""
        if not self._ctrl_stack:
            raise ValueError("end_if_ without matching if_")
        block = self._ctrl_stack.pop()
        block.closed = True
        for branch in block.branches:
            has_ops = any(branch.ctrl_field in op.skip for op in self._ops)
            has_subflows = any(
                branch.ctrl_field in sf._parent_skips for sf in self._sub_flows
            )
            if not has_ops and not has_subflows:
                raise ValueError(
                    f"empty {branch.kind} branch: no operators under "
                    f"condition field {branch.ctrl_field!r}"
                )
        return self


class SubFlow(_FlowBase):
    """A reusable fragment of operator declarations.

    SubFlows don't declare input/output contracts — that's done at the
    top-level Flow.
    """
    pass


class Flow(_FlowBase):
    """A complete pipeline declaration with input/output contract."""

    def __init__(
        self,
        name: str,
        common_input: list[str] | None = None,
        item_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_output: list[str] | None = None,
        sub_flows: list[SubFlow] | None = None,
        storage_mode: str | None = None,
        log_prefix: str | None = None,
        debug: bool = False,
        skip_dead_code: bool = False,
    ):
        super().__init__(name)
        self._common_input = common_input or []
        self._item_input = item_input or []
        self._common_output = common_output
        self._item_output = item_output
        for sf in (sub_flows or []):
            self.add_subflow(sf)
        self._resources: list[ResourceDecl] = []
        _VALID_STORAGE_MODES = {"row", "column"}
        if storage_mode is not None and storage_mode not in _VALID_STORAGE_MODES:
            raise ValueError(
                f"invalid storage_mode={storage_mode!r}, "
                f"must be one of {sorted(_VALID_STORAGE_MODES)}"
            )
        self._storage_mode = storage_mode
        self._log_prefix = log_prefix
        self._debug = debug
        self._skip_dead_code = skip_dead_code

    def resource(self, name: str, res: BaseResource) -> Flow:
        """Declare a resource this flow depends on."""
        self._resources.append(ResourceDecl(
            name=name,
            resource_type=res.resource_type,
            interval=res.interval,
            params=res.params,
        ))
        return self

    def compile(self) -> str:
        """Compile this flow to a JSON config string."""
        return compile_to_json(self)

    def compile_dict(self) -> dict[str, Any]:
        """Compile this flow to a JSON config dict."""
        return compile_flow(self)
