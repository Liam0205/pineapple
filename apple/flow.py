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
        recall=True,
    )
    flow.filter_condition(
        item_input=["item_status"],
        field="item_status", value="offline",
    )
    json_config = flow.compile()
"""
from __future__ import annotations

import sys
from typing import Any

from apple.base import BaseOp, OpCall
from apple.compiler import compile_flow, compile_to_json
from apple.control import (
    ControlBlock,
    ControlBranch,
    extract_fields,
    make_control_op,
)


class _FlowBase:
    """Shared chaining logic for Flow and SubFlow."""

    def __init__(self, name: str):
        self._name = name
        self._ops: list[OpCall] = []
        # Control flow state
        self._ctrl_counter = 0
        self._ctrl_stack: list[ControlBlock] = []

    def __getattr__(self, name: str) -> Any:
        """Dynamic operator dispatch: flow.some_op(...) creates an OpCall."""
        if name.startswith("_"):
            raise AttributeError(name)

        def op_caller(**kwargs: Any) -> _FlowBase:
            return self._add_op(name, **kwargs)

        return op_caller

    def _add_op(self, type_name: str, **kwargs: Any) -> _FlowBase:
        """Record an operator call."""
        # Separate metadata kwargs from business params
        meta_keys = {
            "common_input", "common_output", "item_input", "item_output",
            "item_defaults", "recall", "sources",
        }
        meta = {}
        params = {}
        for k, v in kwargs.items():
            if k in meta_keys:
                meta[k] = v
            else:
                params[k] = v

        call = OpCall(
            type_name=type_name,
            params=params,
            common_input=meta.get("common_input", []),
            common_output=meta.get("common_output", []),
            item_input=meta.get("item_input", []),
            item_output=meta.get("item_output", []),
            item_defaults=meta.get("item_defaults"),
            recall=meta.get("recall", False),
            sources=meta.get("sources"),
        )

        # Apply skip field if inside a control block
        if self._ctrl_stack:
            block = self._ctrl_stack[-1]
            if block.branches:
                branch = block.branches[-1]
                call.skip = branch.ctrl_field
                if branch.ctrl_field not in call.common_input:
                    call.common_input = [branch.ctrl_field] + call.common_input

        self._ops.append(call)
        return self

    # --- Control flow ---

    def if_(self, condition: str) -> _FlowBase:
        """Start an if block."""
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
        self._ops.append(ctrl_op)
        return self

    def elseif_(self, condition: str) -> _FlowBase:
        """Add an elseif branch."""
        if not self._ctrl_stack:
            raise ValueError("elseif_ without matching if_")
        block = self._ctrl_stack[-1]
        if block.closed:
            raise ValueError("elseif_ after end_if_")

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
        self._ops.append(ctrl_op)
        return self

    def else_(self) -> _FlowBase:
        """Add an else branch."""
        if not self._ctrl_stack:
            raise ValueError("else_ without matching if_")
        block = self._ctrl_stack[-1]
        if block.closed:
            raise ValueError("else_ after end_if_")

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
        self._ops.append(ctrl_op)
        return self

    def end_if_(self) -> _FlowBase:
        """Close the current if block."""
        if not self._ctrl_stack:
            raise ValueError("end_if_ without matching if_")
        block = self._ctrl_stack.pop()
        block.closed = True
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
    ):
        super().__init__(name)
        self._common_input = common_input or []
        self._item_input = item_input or []
        self._common_output = common_output
        self._item_output = item_output
        self._sub_flows = sub_flows or []

    def compile(self) -> str:
        """Compile this flow to a JSON config string."""
        return compile_to_json(self)

    def compile_dict(self) -> dict[str, Any]:
        """Compile this flow to a JSON config dict."""
        return compile_flow(self)
