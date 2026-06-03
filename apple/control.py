"""Control flow constructs for Apple DSL.

Provides if_ / elseif_ / else_ / end_if_ that compile to Lua control
operators + skip fields, per design_doc/06_json_config.md.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from apple._template_syntax import TEMPLATE_PATTERN, extract_fields
from apple.base import OpCall


@dataclass
class ControlBlock:
    """Tracks state for one if/elseif/else/end_if block."""
    block_id: int  # unique across the flow
    branches: list[ControlBranch] = field(default_factory=list)
    closed: bool = False


@dataclass
class ControlBranch:
    """One branch (if / elseif / else) within a ControlBlock."""
    kind: str  # "if", "elseif", "else"
    condition: str | None  # Lua condition string, None for else
    ctrl_field: str  # e.g. "_if_1", "_elif_2", "_else_3"
    ctrl_index: int  # global control op counter


# `{{field}}` syntax primitives are shared with apple/template.py via
# apple/_template_syntax (issue #76). `extract_fields` is re-exported as-is
# so `from apple.control import extract_fields` callers keep working;
# `_strip_template` stays here because Lua emission is control-only.
def _strip_template(condition: str) -> str:
    """Replace ``{{field}}`` markers with bare field names for Lua emission."""
    return TEMPLATE_PATTERN.sub(r"\1", condition)


def make_control_op(
    branch: ControlBranch,
    prior_ctrl_fields: list[str],
    condition: str | None,
) -> OpCall:
    """Build the Lua OpCall for a control branch."""
    common_input = list(prior_ctrl_fields)
    if condition:
        common_input.extend(extract_fields(condition))
    # Deduplicate while preserving order
    seen: set[str] = set()
    deduped: list[str] = []
    for f in common_input:
        if f not in seen:
            seen.add(f)
            deduped.append(f)
    common_input = deduped

    # Build Lua script (strip {{...}} markers for Lua emission)
    lua_cond = _strip_template(condition) if condition else None
    if branch.kind == "if":
        lua_body = f"if ({lua_cond}) then return false else return true end"
    elif branch.kind == "elseif":
        prior_check = " and ".join(f"({f})" for f in prior_ctrl_fields)
        lua_body = f"if ({prior_check}) and ({lua_cond}) then return false else return true end"
    else:  # else
        prior_check = " and ".join(f"({f})" for f in prior_ctrl_fields)
        lua_body = f"if ({prior_check}) then return false else return true end"

    lua_script = f"function evaluate() {lua_body} end"

    return OpCall(
        type_name="transform_by_lua",
        params={
            "lua_script": lua_script,
            "function_for_item": "",
            "function_for_common": "evaluate",
        },
        common_input=common_input,
        common_output=[branch.ctrl_field],
        for_branch_control=True,
        code_info=f"[{branch.kind}] {condition or ''}",
        name=f"{branch.kind}_{branch.ctrl_index}",
    )
