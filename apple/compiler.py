"""Compile a Flow into the Pine JSON config format.

Steps:
1. Recursively traverse Flow/SubFlow tree, collecting all ops in global order
2. Auto-generate unique operator names: {type_name}_{hash6}
3. Validate field coverage and write-without-read
4. Dead-code detection (if output contract declared)
5. Emit JSON with pipeline_config, pipeline_group, flow_contract
"""
from __future__ import annotations

import json
from copy import deepcopy
from datetime import datetime, timezone
from typing import Any

from apple._version import __version__
from apple.base import OpCall
from apple.validator import (
    ValidationError,
    detect_dead_code,
    validate_data_parallel,
    validate_field_coverage,
    validate_no_underscore_output,
    validate_param_metadata_consistency,
    validate_sources_references,
    validate_write_without_read,
)


def compile_flow(flow: Any) -> dict[str, Any]:
    """Compile a Flow object into a Pine JSON config dict."""
    # 1. Check unclosed control blocks (recursive)
    _check_unclosed_control(flow, flow._name)

    # 2. Recursive structure traversal
    all_ops: list[OpCall] = []
    structures: dict[str, list[tuple[str, Any]]] = {}
    visited: set[int] = set()
    exclusion_groups: list[set[str]] = []
    _traverse(flow, "", all_ops, structures, visited, exclusion_groups=exclusion_groups)

    # 3. Generate unique names
    named_ops: list[tuple[str, OpCall]] = []
    name_counts: dict[str, int] = {}
    for op in all_ops:
        name = op.unique_name()
        if op.name:
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

    # 4. Validate
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
        exclusion_groups=exclusion_groups,
    )
    validate_data_parallel(named_ops)
    validate_sources_references(named_ops)
    validate_param_metadata_consistency(named_ops)
    dead = detect_dead_code(
        named_ops,
        flow._common_output,
        flow._item_output,
    )
    if dead and not getattr(flow, '_skip_dead_code', False):
        raise ValidationError(
            f"dead operators detected (output not consumed): {dead}"
        )

    # 5. Build operators dict
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
            entry["sources"] = list(op.sources)
        if op.skip:
            entry["skip"] = op.skip
        if op.for_branch_control:
            entry["for_branch_control"] = True
        if op.consumes_row_set:
            entry["consumes_row_set"] = True
        if op.item_defaults:
            entry["item_defaults"] = op.item_defaults
        if op.common_defaults:
            entry["common_defaults"] = op.common_defaults
        if op.debug:
            entry["debug"] = True
        if op.data_parallel > 1:
            entry["data_parallel"] = op.data_parallel
        for k, v in op.params.items():
            entry[k] = v
        operators[name] = entry

    # 6. Build pipeline_map (SubFlow paths only)
    pipeline_map: dict[str, Any] = {}
    for path, entries in structures.items():
        if path == "":
            continue
        pipeline_map[path] = {
            "pipeline": [_resolve_entry(e, named_ops) for e in entries]
        }

    # 6b. Validate no operator name collides with a SubFlow path
    for name, _ in named_ops:
        if name in pipeline_map:
            raise ValidationError(
                f"operator name {name!r} collides with SubFlow path"
            )

    # 7. Build pipeline_group (top-level entries)
    top_entries = structures.get("", [])
    pipeline_group = {
        "main": {
            "pipeline": [_resolve_entry(e, named_ops) for e in top_entries]
        }
    }

    # 8. Build flow_contract
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

    # 9. Validate resource references
    declared_resources: set[str] = set()
    if hasattr(flow, '_resources'):
        declared_resources = {r.name for r in flow._resources}
    _validate_resource_refs(named_ops, declared_resources)

    # 10. Build result
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

    if hasattr(flow, '_storage_mode') and flow._storage_mode:
        result["storage_mode"] = flow._storage_mode
    if hasattr(flow, '_log_prefix') and flow._log_prefix:
        result["log_prefix"] = flow._log_prefix
    if hasattr(flow, '_debug') and flow._debug:
        result["debug"] = True
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


def _check_unclosed_control(node: Any, display_name: str, visited: set[int] | None = None) -> None:
    """Recursively check for unclosed control blocks in all nodes."""
    if visited is None:
        visited = set()
    obj_id = id(node)
    if obj_id in visited:
        return
    visited.add(obj_id)
    if node._ctrl_stack:
        raise ValidationError(
            f"unclosed if_ block in {display_name!r}: "
            f"missing end_if_() for {len(node._ctrl_stack)} block(s)"
        )
    for sf in node._sub_flows:
        _check_unclosed_control(sf, sf._name, visited)


def _rename_field(op: OpCall, old: str, new: str) -> None:
    """Replace all occurrences of a control field name in an OpCall."""
    op.skip = [new if s == old else s for s in op.skip]
    op.common_input = [new if f == old else f for f in op.common_input]
    op.common_output = [new if f == old else f for f in op.common_output]


def _apply_field_renames(fields: list[str], renames: dict[str, str]) -> list[str]:
    """Apply known control-field renames to a field list."""
    return [renames.get(f, f) for f in fields]


def _rename_control_fields(
    local_ops: list[OpCall],
    child_order: list[tuple[str, int]],
    path: str,
) -> dict[str, str]:
    """Prefix control-field names with SubFlow path. Returns rename mapping.

    Only applies when path is non-empty (i.e. inside a SubFlow). For each
    control op (for_branch_control=True with a name), renames its output field
    and propagates the rename to all other local ops.
    """
    if not path:
        return {}
    field_renames: dict[str, str] = {}
    for kind, idx in child_order:
        if kind != "op":
            continue
        op = local_ops[idx]
        if not (op.for_branch_control and op.name):
            continue
        prefix = path.replace("/", "::") + "::"
        old_field = op.common_output[0] if op.common_output else None
        new_field = f"_{prefix}{op.name}" if old_field else None
        if old_field and new_field and old_field != new_field:
            _rename_field(op, old_field, new_field)
            op.name = f"{prefix}{op.name}"
            field_renames[old_field] = new_field
            for other_op in local_ops:
                if other_op is not op:
                    _rename_field(other_op, old_field, new_field)
    return field_renames


def _inject_inherited_skips(
    local_ops: list[OpCall],
    child_order: list[tuple[str, int]],
    inherited_skips: list[str],
) -> None:
    """Append inherited skip fields to each op's skip and common_input.

    Must be called AFTER _rename_control_fields so that inherited fields
    (from an outer branch) are never incorrectly renamed to a local variant.
    """
    if not inherited_skips:
        return
    for kind, idx in child_order:
        if kind != "op":
            continue
        op = local_ops[idx]
        for s in inherited_skips:
            if s not in op.skip:
                op.skip.append(s)
            if s not in op.common_input:
                op.common_input = [s] + op.common_input


def _collect_exclusion_groups(
    node: Any,
    field_renames: dict[str, str],
    exclusion_groups: list[set[str]],
) -> None:
    """Collect mutual-exclusion groups from closed control blocks."""
    for block in getattr(node, '_closed_blocks', []):
        group: set[str] = set()
        for branch in block.branches:
            original = branch.ctrl_field
            renamed = field_renames.get(original, original)
            group.add(renamed)
        if len(group) > 1:
            exclusion_groups.append(group)


def _traverse(
    node: Any,
    path: str,
    all_ops: list[OpCall],
    structures: dict[str, list[tuple[str, Any]]],
    visited: set[int],
    inherited_skips: list[str] | None = None,
    exclusion_groups: list[set[str]] | None = None,
) -> None:
    """Recursively traverse _child_order, collecting ops and structure.

    Execution order:
    1. Rename control fields (local transform, no recursion)
    2. Flatten + recurse into SubFlows (maintains global op ordering)
    3. Inject inherited skips (after rename, so outer fields stay intact)
    4. Collect exclusion groups (after rename)
    """
    if inherited_skips is None:
        inherited_skips = []
    if exclusion_groups is None:
        exclusion_groups = []
    obj_id = id(node)
    if obj_id in visited:
        raise ValidationError(
            f"SubFlow cycle or reuse detected: {node._name!r}"
        )
    visited.add(obj_id)

    local_ops = [deepcopy(op) for op in node._ops]

    # Pass 1: Rename control fields
    field_renames = _rename_control_fields(local_ops, node._child_order, path)

    # Pass 2: Flatten + recurse (preserves global op ordering)
    entries: list[tuple[str, Any]] = []
    for kind, idx in node._child_order:
        if kind == "op":
            op = local_ops[idx]
            op.subflow_path = path
            global_idx = len(all_ops)
            all_ops.append(op)
            entries.append(("op", global_idx))
        else:
            sf = node._sub_flows[idx]
            sf_path = f"{path}/{sf._name}" if path else sf._name
            if sf_path in structures:
                raise ValidationError(
                    f"duplicate SubFlow path: {sf_path!r}"
                )
            entries.append(("sf", sf_path))
            parent_skips = _apply_field_renames(
                getattr(sf, '_parent_skips', []),
                field_renames,
            )
            child_skips = inherited_skips + parent_skips
            _traverse(sf, sf_path, all_ops, structures, visited, child_skips, exclusion_groups)

    # Pass 3: Inject inherited skips (after rename)
    _inject_inherited_skips(local_ops, node._child_order, inherited_skips)

    # Pass 4: Collect exclusion groups (after rename)
    _collect_exclusion_groups(node, field_renames, exclusion_groups)

    structures[path] = entries


def _resolve_entry(
    entry: tuple[str, Any],
    named_ops: list[tuple[str, OpCall]],
) -> str:
    """Resolve a structure entry to its final pipeline string."""
    kind, val = entry
    if kind == "op":
        return named_ops[val][0]
    return val


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
