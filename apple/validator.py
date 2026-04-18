"""DSL-side validation for Apple flows.

Checks:
1. Field coverage: every operator input must come from flow contract or upstream output.
2. Write-without-read: writing a field that already exists without reading it.
3. Dead code detection: operators whose output is not consumed by flow output or downstream.
"""
from __future__ import annotations

from apple.base import OpCall


class ValidationError(Exception):
    """Raised when DSL validation fails."""
    pass


def validate_field_coverage(
    ops: list[tuple[str, OpCall]],
    flow_common_input: list[str],
    flow_item_input: list[str],
) -> None:
    """Check that every operator input is provided by flow contract or upstream output."""
    available_common: set[str] = set(flow_common_input)
    available_item: set[str] = set(flow_item_input)

    for name, op in ops:
        # Check common inputs
        for field in op.common_input:
            if field.startswith("_"):
                continue  # internal control fields handled by compiler
            if field not in available_common:
                raise ValidationError(
                    f"operator {name!r}: common_input field {field!r} not provided "
                    f"by flow contract or upstream output"
                )
        # Check item inputs
        for field in op.item_input:
            if field.startswith("_"):
                continue
            if field not in available_item:
                raise ValidationError(
                    f"operator {name!r}: item_input field {field!r} not provided "
                    f"by flow contract or upstream output"
                )
        # Register outputs
        available_common.update(op.common_output)
        available_item.update(op.item_output)


def validate_write_without_read(
    ops: list[tuple[str, OpCall]],
    flow_common_input: list[str],
    flow_item_input: list[str],
) -> None:
    """Detect writing a field that already exists (from upstream operator output)
    without reading it. Flow-contract inputs are not flagged — operators are
    allowed to output fields that match flow inputs without reading them."""
    # Track fields written by operators (not flow contract)
    written_by_ops_common: set[str] = set()
    written_by_ops_item: set[str] = set()

    for name, op in ops:
        for field in op.common_output:
            if field.startswith("_"):
                continue
            if field in written_by_ops_common and field not in op.common_input:
                raise ValidationError(
                    f"operator {name!r}: writes common field {field!r} "
                    f"without reading it (field already exists from upstream)"
                )
        for field in op.item_output:
            if field.startswith("_"):
                continue
            if field in written_by_ops_item and field not in op.item_input:
                raise ValidationError(
                    f"operator {name!r}: writes item field {field!r} "
                    f"without reading it (field already exists from upstream)"
                )
        written_by_ops_common.update(op.common_output)
        written_by_ops_item.update(op.item_output)


def detect_dead_code(
    ops: list[tuple[str, OpCall]],
    flow_common_output: list[str] | None,
    flow_item_output: list[str] | None,
) -> list[str]:
    """Detect operators whose output is never consumed.

    Returns list of dead operator names.
    An operator's output is "consumed" if it appears in:
    - the flow output contract (common_output / item_output), OR
    - a downstream operator's input.
    If the flow output contract is not declared (None), only downstream
    consumption counts — undelivered fields are dead.
    """
    # Build set of "needed" fields: flow outputs + all operator inputs
    needed_common: set[str] = set(flow_common_output or [])
    needed_item: set[str] = set(flow_item_output or [])

    # Fields consumed by downstream operators are needed
    for _, op in ops:
        needed_common.update(op.common_input)
        needed_item.update(op.item_input)

    dead: list[str] = []
    for name, op in ops:
        if op.for_branch_control:
            continue  # control ops are never dead
        if op.recall:
            continue  # recall ops are always needed
        if not op.common_output and not op.item_output:
            continue  # observe ops (no output at all) are exempt

        produces_needed = False
        for field in op.common_output:
            if field in needed_common:
                produces_needed = True
                break
        if not produces_needed:
            for field in op.item_output:
                if field in needed_item:
                    produces_needed = True
                    break

        if not produces_needed and (op.common_output or op.item_output):
            dead.append(name)

    return dead
