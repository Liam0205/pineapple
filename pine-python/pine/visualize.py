from __future__ import annotations

import re
from dataclasses import dataclass, field

from pine.dag import DAG

_DOT_FILL_COLORS = {
    "recall": "#E8F5E9",
    "transform": "#E3F2FD",
    "filter": "#FFF3E0",
    "merge": "#F3E5F5",
    "reorder": "#FFFDE7",
    "observe": "#F5F5F5",
}

_MERMAID_CLASS_DEFS = [
    "classDef recall fill:#E8F5E9,stroke:#4CAF50",
    "classDef transform fill:#E3F2FD,stroke:#2196F3",
    "classDef filter fill:#FFF3E0,stroke:#FF9800",
    "classDef merge fill:#F3E5F5,stroke:#9C27B0",
    "classDef reorder fill:#FFFDE7,stroke:#FFC107",
    "classDef observe fill:#F5F5F5,stroke:#9E9E9E",
]

_SANITIZE_RE = re.compile(r"[^a-zA-Z0-9_]")


def _sanitize_id(name: str) -> str:
    return _SANITIZE_RE.sub("_", name)


def render_dot(dag: DAG) -> str:
    lines: list[str] = []
    lines.append("digraph pipeline {")
    lines.append('    rankdir=TB;')
    lines.append('    node [shape=box, style=filled, fontname="Helvetica"];')
    lines.append("")

    for node in dag.nodes:
        color = _DOT_FILL_COLORS.get(node.config.operator_type, "#FFFFFF")
        lines.append(f'    "{node.name}" [label="{node.name}", fillcolor="{color}"];')

    lines.append("")

    for node in dag.nodes:
        for succ_idx in node.succs:
            succ = dag.nodes[succ_idx]
            lines.append(f'    "{node.name}" -> "{succ.name}";')

    lines.append("}")
    return "\n".join(lines) + "\n"


def render_mermaid(dag: DAG) -> str:
    lines: list[str] = []
    lines.append("graph TB")

    for node in dag.nodes:
        sid = _sanitize_id(node.name)
        op_type = node.config.operator_type or "transform"
        lines.append(f'    {sid}["{node.name}"]:::{op_type}')

    lines.append("")

    for node in dag.nodes:
        src_id = _sanitize_id(node.name)
        for succ_idx in node.succs:
            succ = dag.nodes[succ_idx]
            dst_id = _sanitize_id(succ.name)
            lines.append(f"    {src_id} --> {dst_id}")

    lines.append("")
    for cd in _MERMAID_CLASS_DEFS:
        lines.append(f"    {cd}")

    return "\n".join(lines) + "\n"


@dataclass
class _CollapsedGroup:
    key: str
    label: str
    is_subflow: bool
    node_indices: list[int] = field(default_factory=list)


def _collapse_key(sub_flow: str, level: int) -> str:
    if not sub_flow:
        return ""
    parts = sub_flow.split("/")
    if level >= len(parts):
        return sub_flow
    return "/".join(parts[:level])


def _build_collapsed(dag: DAG, level: int) -> tuple[list[_CollapsedGroup], list[tuple[int, int]]]:
    key_to_group_idx: dict[str, int] = {}
    groups: list[_CollapsedGroup] = []
    node_to_group: list[int] = []

    for node in dag.nodes:
        key = _collapse_key(node.sub_flow, level)
        if key == "":
            gidx = len(groups)
            groups.append(_CollapsedGroup(
                key=f"_standalone_{node.index}",
                label=node.name,
                is_subflow=False,
                node_indices=[node.index],
            ))
            node_to_group.append(gidx)
        elif key in key_to_group_idx:
            gidx = key_to_group_idx[key]
            groups[gidx].node_indices.append(node.index)
            node_to_group.append(gidx)
        else:
            gidx = len(groups)
            key_to_group_idx[key] = gidx
            groups.append(_CollapsedGroup(
                key=key,
                label=key,
                is_subflow=True,
                node_indices=[node.index],
            ))
            node_to_group.append(gidx)

    edges: list[tuple[int, int]] = []
    seen_edges: set[tuple[int, int]] = set()
    for node in dag.nodes:
        from_group = node_to_group[node.index]
        for succ_idx in node.succs:
            to_group = node_to_group[succ_idx]
            if from_group != to_group:
                edge = (from_group, to_group)
                if edge not in seen_edges:
                    seen_edges.add(edge)
                    edges.append(edge)

    return groups, edges


def render_collapsed_dot(dag: DAG, level: int) -> str:
    groups, edges = _build_collapsed(dag, level)

    lines: list[str] = []
    lines.append("digraph pipeline {")
    lines.append('    rankdir=TB;')
    lines.append('    node [shape=box, style=filled, fontname="Helvetica"];')
    lines.append("")

    for i, group in enumerate(groups):
        color = "#BBDEFB" if group.is_subflow else "#E0E0E0"
        lines.append(f'    "g{i}" [label="{group.label}", fillcolor="{color}"];')

    lines.append("")

    for from_g, to_g in edges:
        lines.append(f'    "g{from_g}" -> "g{to_g}";')

    lines.append("}")
    return "\n".join(lines) + "\n"


def render_collapsed_mermaid(dag: DAG, level: int) -> str:
    groups, edges = _build_collapsed(dag, level)

    lines: list[str] = []
    lines.append("graph TB")

    for i, group in enumerate(groups):
        cls = "subflow" if group.is_subflow else "standalone"
        lines.append(f'    g{i}["{group.label}"]:::{cls}')

    lines.append("")

    for from_g, to_g in edges:
        lines.append(f"    g{from_g} --> g{to_g}")

    lines.append("")
    lines.append("    classDef subflow fill:#BBDEFB,stroke:#1976D2")
    lines.append("    classDef standalone fill:#E0E0E0,stroke:#616161")

    return "\n".join(lines) + "\n"
