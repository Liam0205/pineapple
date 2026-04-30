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


def _op_location(name: str, op: OpCall) -> str:
    """Build a human-friendly location prefix for operator error messages."""
    header = f"operator {name!r}"
    if op.subflow_path:
        header += f" [{op.subflow_path}]"
    if op.code_info:
        return f"{header}:\n  defined at: {op.code_info}\n  "
    return f"{header}: "


def validate_no_underscore_output(
    ops: list[tuple[str, OpCall]],
    flow_common_output: list[str] | None,
    flow_item_output: list[str] | None,
) -> None:
    """Reject underscore-prefixed field names in user-declared outputs.

    The ``_`` prefix is reserved for engine internals (control-flow fields like
    ``_if_1``, runtime fields like ``_source``).  Users may *read* internal
    fields via ``common_input`` / ``item_input``, but must not *write* them
    via ``common_output`` / ``item_output``.

    Control operators (``for_branch_control=True``) are exempt because their
    ``_if_*`` / ``_elif_*`` / ``_else_*`` outputs are compiler-generated.
    """
    # Flow-level output contract
    for field in (flow_common_output or []):
        if field.startswith("_"):
            raise ValidationError(
                f"flow common_output field {field!r} starts with '_', "
                f"which is reserved for engine internals"
            )
    for field in (flow_item_output or []):
        if field.startswith("_"):
            raise ValidationError(
                f"flow item_output field {field!r} starts with '_', "
                f"which is reserved for engine internals"
            )

    # Per-operator output
    for name, op in ops:
        if op.for_branch_control:
            continue  # compiler-generated control ops are exempt
        for field in op.common_output:
            if field.startswith("_"):
                raise ValidationError(
                    f"{_op_location(name, op)}common_output field {field!r} starts "
                    f"with '_', which is reserved for engine internals"
                )
        for field in op.item_output:
            if field.startswith("_"):
                raise ValidationError(
                    f"{_op_location(name, op)}item_output field {field!r} starts "
                    f"with '_', which is reserved for engine internals"
                )


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
                    f"{_op_location(name, op)}common_input field {field!r} not provided "
                    f"by flow contract or upstream output"
                )
        # Check item inputs
        for field in op.item_input:
            if field.startswith("_"):
                continue
            if field not in available_item:
                raise ValidationError(
                    f"{_op_location(name, op)}item_input field {field!r} not provided "
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
    allowed to output fields that match flow inputs without reading them.

    Operators inside a control-flow branch (``skip`` is set) are exempt from
    *being flagged* AND their outputs do not count as "already written" for
    downstream checks.  This is because mutually exclusive branches (if/else)
    may legitimately write the same field without reading it."""
    # Track fields written by operators (not flow contract).
    # Only unconditional (non-skip) operators contribute.
    written_by_ops_common: set[str] = set()
    written_by_ops_item: set[str] = set()

    for name, op in ops:
        if op.skip:
            continue  # branch-internal ops are exempt
        for field in op.common_output:
            if field.startswith("_"):
                continue
            if field in written_by_ops_common and field not in op.common_input:
                raise ValidationError(
                    f"{_op_location(name, op)}writes common field {field!r} "
                    f"without reading it (field already exists from upstream)"
                )
        for field in op.item_output:
            if field.startswith("_"):
                continue
            if field in written_by_ops_item and field not in op.item_input:
                raise ValidationError(
                    f"{_op_location(name, op)}writes item field {field!r} "
                    f"without reading it (field already exists from upstream)"
                )
        written_by_ops_common.update(op.common_output)
        written_by_ops_item.update(op.item_output)


def validate_data_parallel(ops: list[tuple[str, OpCall]]) -> None:
    """Check data_parallel constraints at compile time.

    When data_parallel > 1:
    1. The operator must be a Transform (type_name starts with "transform_").
    2. common_output must be empty.
    """
    for name, op in ops:
        if op.data_parallel > 1:
            if not op.type_name.startswith("transform_"):
                raise ValidationError(
                    f"{_op_location(name, op)}data_parallel={op.data_parallel} "
                    f"is only supported for Transform operators, "
                    f"got type_name={op.type_name!r}"
                )
            if op.common_output:
                raise ValidationError(
                    f"{_op_location(name, op)}data_parallel={op.data_parallel} "
                    f"requires empty common_output for Transform operators"
                )
            if op.type_name in _DATA_PARALLEL_UNSAFE_TRANSFORMS:
                raise ValidationError(
                    f"{_op_location(name, op)}data_parallel={op.data_parallel} "
                    f"is not supported for operator {op.type_name!r} because "
                    "it requires whole-item-set semantics"
                )


_DATA_PARALLEL_UNSAFE_TRANSFORMS: set[str] = {
    "transform_normalize",
}


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


_PARAM_METADATA_RULES: dict[str, list[tuple[str, str]]] = {
    "transform_resource_lookup": [
        ("lookup_key", "item_input"),
        ("output_field", "item_output"),
    ],
}


def validate_param_metadata_consistency(
    ops: list[tuple[str, OpCall]],
) -> None:
    """Check that business params implying metadata fields are consistent.

    For example, transform_resource_lookup's lookup_key must appear in
    item_input, and output_field must appear in item_output.
    """
    for name, op in ops:
        rules = _PARAM_METADATA_RULES.get(op.type_name)
        if not rules:
            continue
        for param_name, metadata_attr in rules:
            value = op.params.get(param_name)
            if value and value not in getattr(op, metadata_attr, []):
                raise ValidationError(
                    f"{_op_location(name, op)}param {param_name!r}={value!r} "
                    f"must appear in {metadata_attr}"
                )
