"""Compile a Flow into the Pine JSON config format.

Steps:
1. Flatten sub_flows into single operator sequence
2. Lower control flow (if_/elseif_/else_/end_if_) to Lua operators + skip fields
3. Auto-generate unique operator names: {type_name}_{hash6}
4. Validate field coverage and write-without-read
5. Dead-code detection (if output contract declared)
6. Emit JSON with pipeline_config, pipeline_group, flow_contract
"""
from __future__ import annotations

import json
from datetime import datetime, timezone
from typing import Any

from apple.base import OpCall
from apple._version import __version__
from apple.validator import (
    ValidationError,
    detect_dead_code,
    validate_field_coverage,
    validate_no_underscore_output,
    validate_write_without_read,
)


def compile_flow(flow: Any) -> dict[str, Any]:
    """Compile a Flow object into a Pine JSON config dict."""
    # 1. Flatten ops from flow + sub_flows
    all_ops: list[OpCall] = []
    sub_flow_boundaries: dict[str, list[int]] = {}  # name -> [start, end)

    if flow._sub_flows:
        for sf in flow._sub_flows:
            start = len(all_ops)
            all_ops.extend(sf._ops)
            sub_flow_boundaries[sf._name] = [start, len(all_ops)]
    if flow._ops:
        start = len(all_ops)
        all_ops.extend(flow._ops)
        sub_flow_boundaries[f"_main_{flow._name}"] = [start, len(all_ops)]

    # 1b. Check for unclosed control blocks
    if flow._ctrl_stack:
        raise ValidationError(
            f"unclosed if_ block in flow {flow._name!r}: "
            f"missing end_if_() for {len(flow._ctrl_stack)} block(s)"
        )
    if flow._sub_flows:
        for sf in flow._sub_flows:
            if sf._ctrl_stack:
                raise ValidationError(
                    f"unclosed if_ block in sub_flow {sf._name!r}: "
                    f"missing end_if_() for {len(sf._ctrl_stack)} block(s)"
                )

    # 2. Generate unique names
    named_ops: list[tuple[str, OpCall]] = []
    name_counts: dict[str, int] = {}
    for op in all_ops:
        name = op.unique_name()
        if op.name:
            # Explicit name — must be unique
            if name in name_counts:
                raise ValidationError(
                    f"duplicate explicit operator name: {name!r}"
                )
        if name in name_counts:
            name_counts[name] += 1
            name = f"{name}_{name_counts[name]}"
        else:
            name_counts[name] = 0
        named_ops.append((name, op))

    # 3. Validate
    validate_no_underscore_output(
        named_ops,
        flow._common_output,
        flow._item_output,
    )
    validate_field_coverage(
        named_ops,
        flow._common_input or [],
        flow._item_input or [],
    )
    validate_write_without_read(
        named_ops,
        flow._common_input or [],
        flow._item_input or [],
    )
    dead = detect_dead_code(
        named_ops,
        flow._common_output,
        flow._item_output,
    )
    if dead:
        raise ValidationError(
            f"dead operators detected (output not consumed): {dead}"
        )

    # 4. Build operators dict
    operators: dict[str, Any] = {}
    for name, op in named_ops:
        entry: dict[str, Any] = {
            "type_name": op.type_name,
            "$metadata": {
                "common_input": op.common_input,
                "common_output": op.common_output,
                "item_input": op.item_input,
                "item_output": op.item_output,
            },
        }
        if op.code_info:
            entry["$code_info"] = op.code_info
        if op.recall:
            entry["recall"] = True
        if op.sources:
            entry["sources"] = [
                _resolve_source(op.sources, named_ops, s)
                for s in op.sources
            ]
        if op.skip:
            entry["skip"] = op.skip
        if op.for_branch_control:
            entry["for_branch_control"] = True
        if op.row_dependency:
            entry["row_dependency"] = True
        if op.item_defaults:
            entry["item_defaults"] = op.item_defaults
        if op.common_defaults:
            entry["common_defaults"] = op.common_defaults
        if op.debug:
            entry["debug"] = True
        # Business params
        for k, v in op.params.items():
            entry[k] = v
        operators[name] = entry

    # 5. Build pipeline_map
    pipeline_map: dict[str, Any] = {}
    for sf_name, (start, end) in sub_flow_boundaries.items():
        pipeline_map[sf_name] = {
            "pipeline": [named_ops[i][0] for i in range(start, end)]
        }

    # 6. Build pipeline_group
    pipeline_group = {
        "main": {
            "pipeline": list(pipeline_map.keys())
        }
    }

    # 7. Build flow_contract
    flow_contract: dict[str, Any] = {
        "common_input": flow._common_input or [],
        "item_input": flow._item_input or [],
    }
    if flow._common_output is not None:
        flow_contract["common_output"] = flow._common_output
    else:
        flow_contract["common_output"] = []
    if flow._item_output is not None:
        flow_contract["item_output"] = flow._item_output
    else:
        flow_contract["item_output"] = []

    # 8. Validate resource references
    declared_resources: set[str] = set()
    if hasattr(flow, '_resources'):
        declared_resources = {r.name for r in flow._resources}
    _validate_resource_refs(named_ops, declared_resources)

    # 9. Build result
    result: dict[str, Any] = {
        "_PINEAPPLE_VERSION": __version__,
        "_PINEAPPLE_CREATE_TIME": datetime.now(timezone.utc).strftime(
            "%Y-%m-%dT%H:%M:%SZ"
        ),
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": pipeline_map,
        },
        "pipeline_group": pipeline_group,
        "flow_contract": flow_contract,
    }

    # 10. Add resource_config if resources declared
    if hasattr(flow, '_resources') and flow._resources:
        result["resource_config"] = {
            r.name: {
                "type": r.resource_type,
                "interval": r.interval,
                "params": r.params,
            }
            for r in flow._resources
        }

    return result


def _validate_resource_refs(
    named_ops: list[tuple[str, OpCall]],
    declared_resources: set[str],
) -> None:
    """Validate that all resource_name params reference declared resources."""
    for op_name, op in named_ops:
        res_name = op.params.get("resource_name")
        if res_name is not None and res_name not in declared_resources:
            raise ValidationError(
                f"operator {op_name!r} references resource_name={res_name!r} "
                f"but no such resource was declared via flow.resource(). "
                f"Declared resources: {sorted(declared_resources) or '(none)'}"
            )


def compile_to_json(flow: Any, indent: int = 2) -> str:
    """Compile a Flow and return the JSON string."""
    return json.dumps(compile_flow(flow), indent=indent, ensure_ascii=False)


def _resolve_source(
    source_refs: list[str],
    named_ops: list[tuple[str, OpCall]],
    source_type_hint: str,
) -> str:
    """Pass through the source operator name.

    Source refs are already final operator names provided by the user
    via the sources=[...] metadata kwarg in the DSL. No resolution needed.
    """
    return source_type_hint
