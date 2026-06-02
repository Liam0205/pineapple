"""DSL-side validation for Apple flows.

Checks:
1. Field coverage: every operator input must come from flow contract or upstream output.
2. Write-without-read: writing a field that already exists without reading it.
3. Dead code detection: operators whose output is not consumed by flow output or downstream.
"""
from __future__ import annotations

from apple.base import OpCall
from apple.template import extract_fields, is_bare_template, is_templated


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
    """Check that every operator input is provided by flow contract or upstream output.

    Covers all three common_input buckets introduced in #74: the business
    list, the skip-control list, and the template-source list. Skip
    fields start with ``_`` and are filtered by the engine; template
    fields are business names that must resolve to an upstream/contract
    field exactly like ``common_input`` entries.
    """
    available_common: set[str] = set(flow_common_input)
    available_item: set[str] = set(flow_item_input)

    for name, op in ops:
        # Check common inputs — union of all three buckets (#74).
        union_common = (
            list(op.common_input)
            + list(op.common_input_skip)
            + list(op.common_input_template)
        )
        for field in union_common:
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
    exclusion_groups: list[set[str]] | None = None,
) -> None:
    """Detect writing a field that already exists (from upstream operator output)
    without reading it. Flow-contract inputs are not flagged — operators are
    allowed to output fields that match flow inputs without reading them.

    Operators in mutually exclusive branches (same ControlBlock, different
    branches) may write the same field without reading it.  Two independent
    if blocks are NOT mutually exclusive and will be flagged."""
    groups = exclusion_groups or []

    def _are_mutually_exclusive(skip_a: list[str], skip_b: list[str]) -> bool:
        for group in groups:
            fields_a = group & set(skip_a)
            fields_b = group & set(skip_b)
            if fields_a and fields_b and fields_a != fields_b:
                return True
        return False

    writers_common: dict[str, tuple[str, list[str]]] = {}
    writers_item: dict[str, tuple[str, list[str]]] = {}

    for name, op in ops:
        # Union of all three common_input buckets (#74): a field declared
        # in any bucket counts as "read" for the write-without-read check.
        op_common_reads = (
            set(op.common_input)
            | set(op.common_input_skip)
            | set(op.common_input_template)
        )
        for field in op.common_output:
            if field.startswith("_"):
                continue
            if field in writers_common:
                prior_name, prior_skip = writers_common[field]
                if not _are_mutually_exclusive(prior_skip, op.skip):
                    if field not in op_common_reads:
                        raise ValidationError(
                            f"{_op_location(name, op)}writes common field {field!r} "
                            f"without reading it (field already exists from upstream)"
                        )
            writers_common[field] = (name, list(op.skip))
        for field in op.item_output:
            if field.startswith("_"):
                continue
            if field in writers_item and not op.additive_writes_row_set:
                prior_name, prior_skip = writers_item[field]
                if not _are_mutually_exclusive(prior_skip, op.skip):
                    if field not in op.item_input:
                        raise ValidationError(
                            f"{_op_location(name, op)}writes item field {field!r} "
                            f"without reading it (field already exists from upstream)"
                        )
            writers_item[field] = (name, list(op.skip))


def validate_data_parallel(ops: list[tuple[str, OpCall]]) -> None:
    """Check data_parallel structural constraints at compile time.

    When data_parallel > 1:
    1. The operator must be a Transform (type_name starts with "transform_").
    2. common_output must be empty.

    Capability check (ConcurrentSafe interface) is enforced on the Go side
    at engine build time, eliminating the need for a Python-side blocklist.
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

    # Fields consumed by downstream operators are needed. Union all
    # three common_input buckets so template-source/skip-control
    # dependencies still count as "needed" (#74).
    for _, op in ops:
        needed_common.update(op.common_input)
        needed_common.update(op.common_input_skip)
        needed_common.update(op.common_input_template)
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


def validate_sources_references(ops: list[tuple[str, OpCall]]) -> None:
    """Check that every sources entry refers to an operator declared before the current one.

    This catches two classes of errors in a single pass:
    1. References to non-existent operators (typos, forgotten name=).
    2. Forward references to operators declared after the current one,
       which would create causal inversions in the DAG.
    """
    all_names = {name for name, _ in ops}
    seen: set[str] = set()
    for name, op in ops:
        if op.sources:
            for src in op.sources:
                if src not in seen:
                    if src in all_names:
                        raise ValidationError(
                            f"{_op_location(name, op)}sources references {src!r} "
                            f"which is declared after the current operator "
                            f"(forward reference)"
                        )
                    else:
                        raise ValidationError(
                            f"{_op_location(name, op)}sources references {src!r} "
                            f"which does not exist in the pipeline"
                        )
        seen.add(name)


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


# Scalar param types that are eligible for template interpolation (issue #74).
# The string template is resolved at runtime against the common frame and
# coerced to the declared type before being handed to the operator.
_TEMPLATABLE_SCALAR_TYPES: frozenset[str] = frozenset({
    "string", "int", "int64", "float", "float64", "bool",
})


def _lookup_params_schema(type_name: str) -> dict[str, dict] | None:
    """Look up a codegen-generated operator's ``_params_schema``.

    Returns None when ``apple_generated`` is unavailable (e.g., codegen has
    not been run in this checkout) or the operator is unknown. Templated
    param validation degrades gracefully in that case; the runtimes will
    still reject malformed templates at config load.
    """
    try:
        from apple_generated import operators as _ops  # type: ignore
    except ImportError:
        return None
    for cls_name in getattr(_ops, "__all__", dir(_ops)):
        cls = getattr(_ops, cls_name, None)
        if cls is None or not hasattr(cls, "_name") or not hasattr(cls, "_params_schema"):
            continue
        if cls._name == type_name:
            return cls._params_schema
    return None


def validate_templated_params(ops: list[tuple[str, OpCall]]) -> None:
    """Ensure ``{{field}}`` markers target params that opted into templating.

    For each templated string value in ``op.params``, check that the codegen
    schema declares ``"templatable": True`` for that param and that the
    declared type is a templatable scalar (string / int / int64 / float /
    float64 / bool). Raises :class:`ValidationError` on any violation.

    Silently skips when ``apple_generated`` is not importable so that pre-
    codegen development workflows still succeed.
    """
    for name, op in ops:
        templated_in_op = {
            k: v for k, v in op.params.items() if is_templated(v)
        }
        if not templated_in_op:
            continue
        schema = _lookup_params_schema(op.type_name)
        if schema is None:
            continue
        for param_name, value in templated_in_op.items():
            if not is_bare_template(value):
                raise ValidationError(
                    f"{_op_location(name, op)}templated value {value!r} for "
                    f"param {param_name!r} is not a bare ``{{{{field}}}}`` "
                    f"marker; the L0 template contract forbids literal text "
                    f"or multiple markers in the value — compose strings in "
                    f"an upstream operator (e.g. transform_by_lua) and bind "
                    f"the result via a bare marker"
                )
            spec = schema.get(param_name)
            if spec is None:
                raise ValidationError(
                    f"{_op_location(name, op)}templated value {value!r} targets "
                    f"unknown param {param_name!r} for operator {op.type_name!r}"
                )
            if not spec.get("templatable", False):
                raise ValidationError(
                    f"{_op_location(name, op)}param {param_name!r} on operator "
                    f"{op.type_name!r} does not opt into template interpolation; "
                    f"the operator must declare Templatable=True in its ParamSpec "
                    f"to accept {value!r}"
                )
            declared_type = spec.get("type", "")
            if declared_type not in _TEMPLATABLE_SCALAR_TYPES:
                raise ValidationError(
                    f"{_op_location(name, op)}param {param_name!r} on operator "
                    f"{op.type_name!r} has declared type {declared_type!r}; "
                    f"template interpolation is only supported for scalar types "
                    f"({sorted(_TEMPLATABLE_SCALAR_TYPES)})"
                )
            # Sanity: at least one valid field reference (already ensured by
            # is_templated, but guard against pathological values like "{{}}").
            if not extract_fields(value):
                raise ValidationError(
                    f"{_op_location(name, op)}param {param_name!r}={value!r} "
                    f"contains malformed template marker(s)"
                )
