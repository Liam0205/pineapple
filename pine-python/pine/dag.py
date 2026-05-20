from __future__ import annotations

from collections import deque

from pine.config import OperatorConfig
from pine.errors import ConfigError

_ROW_SET_SENTINEL = "_row_set_"


class _FieldTracker:
    def __init__(self):
        self.last_mut_writer: int = -1
        self.additive_writers: list[int] = []
        self.active_readers: list[int] = []


class Node:
    """Single operator node within the DAG."""

    def __init__(self, name: str, index: int, sub_flow: str, config: OperatorConfig):
        self.name = name
        self.index = index
        self.sub_flow = sub_flow
        self.config = config
        self.preds: list[int] = []
        self.succs: list[int] = []


class DAG:
    """Directed acyclic graph encoding operator execution dependencies."""

    def __init__(self, nodes: list[Node], name_to_index: dict[str, int]):
        self.nodes = nodes
        self.name_to_index = name_to_index

    @classmethod
    def build(
        cls,
        sequence: list[str],
        operators: dict[str, OperatorConfig],
        op_to_sub_flow: dict[str, str],
    ) -> "DAG":
        nodes: list[Node] = []
        name_to_index: dict[str, int] = {}

        for i, name in enumerate(sequence):
            op_cfg = operators.get(name)
            if op_cfg is None:
                raise ConfigError(f'operator "{name}" not found')
            node = Node(name, i, op_to_sub_flow.get(name, ""), op_cfg)
            nodes.append(node)
            name_to_index[name] = i

        g = cls(nodes, name_to_index)

        _add_edges(g, sequence, operators, is_common=True)
        _add_edges(g, sequence, operators, is_common=False)

        for i, name in enumerate(sequence):
            op_cfg = operators[name]
            for src in op_cfg.sources:
                src_idx = name_to_index.get(src)
                if src_idx is None:
                    raise ConfigError(
                        f'operator "{name}" sources references unknown operator "{src}"'
                    )
                _add_edge(g, src_idx, i)

        _reduce(g)
        _topological_sort(g)

        return g

    def topological_order(self) -> list[int]:
        return _topological_sort(self)


def _add_edge(g: DAG, from_idx: int, to_idx: int):
    if from_idx == to_idx:
        return
    if to_idx in g.nodes[from_idx].succs:
        return
    g.nodes[from_idx].succs.append(to_idx)
    g.nodes[to_idx].preds.append(from_idx)


def _add_edges(
    g: DAG,
    sequence: list[str],
    operators: dict[str, OperatorConfig],
    is_common: bool,
):
    fields: dict[str, _FieldTracker] = {}

    for i, name in enumerate(sequence):
        op_cfg = operators[name]
        meta = op_cfg.metadata

        read_fields = list(meta.common_input if is_common else meta.item_input)
        write_fields = list(meta.common_output if is_common else meta.item_output)
        is_additive_write = not is_common and op_cfg.additive_writes_row_set

        if not is_common:
            if is_additive_write:
                write_fields.append(_ROW_SET_SENTINEL)
            if op_cfg.consumes_row_set:
                read_fields.append(_ROW_SET_SENTINEL)
            if not op_cfg.consumes_row_set and not is_additive_write:
                if read_fields or write_fields:
                    read_fields.append(_ROW_SET_SENTINEL)

        # RAW edges
        for field in read_fields:
            ft = fields.setdefault(field, _FieldTracker())
            if ft.last_mut_writer >= 0:
                _add_edge(g, ft.last_mut_writer, i)
            for aw in ft.additive_writers:
                _add_edge(g, aw, i)
            ft.active_readers.append(i)

        # WAR + WAW edges
        for field in write_fields:
            ft = fields.setdefault(field, _FieldTracker())
            if is_additive_write:
                if ft.last_mut_writer >= 0:
                    _add_edge(g, ft.last_mut_writer, i)
                for reader in ft.active_readers:
                    if reader != i:
                        _add_edge(g, reader, i)
                ft.additive_writers.append(i)
            else:
                if ft.last_mut_writer >= 0:
                    _add_edge(g, ft.last_mut_writer, i)
                for aw in ft.additive_writers:
                    _add_edge(g, aw, i)
                for reader in ft.active_readers:
                    if reader != i:
                        _add_edge(g, reader, i)
                ft.last_mut_writer = i
                ft.additive_writers.clear()
                ft.active_readers.clear()

        # MutatesRowSet: mutating write to _ROW_SET_SENTINEL
        if not is_common and op_cfg.mutates_row_set:
            ft = fields.setdefault(_ROW_SET_SENTINEL, _FieldTracker())
            if ft.last_mut_writer >= 0:
                _add_edge(g, ft.last_mut_writer, i)
            for aw in ft.additive_writers:
                _add_edge(g, aw, i)
            for reader in ft.active_readers:
                if reader != i:
                    _add_edge(g, reader, i)
            ft.last_mut_writer = i
            ft.additive_writers.clear()
            ft.active_readers.clear()


def _topological_sort(g: DAG) -> list[int]:
    n = len(g.nodes)
    in_degree = [len(node.preds) for node in g.nodes]
    queue: deque[int] = deque()
    for i in range(n):
        if in_degree[i] == 0:
            queue.append(i)

    order: list[int] = []
    while queue:
        curr = queue.popleft()
        order.append(curr)
        for succ in g.nodes[curr].succs:
            in_degree[succ] -= 1
            if in_degree[succ] == 0:
                queue.append(succ)

    if len(order) != n:
        cycle_nodes = [g.nodes[i].name for i in range(n) if in_degree[i] > 0]
        raise ConfigError(
            f"DAG contains a cycle involving operators: {cycle_nodes}"
        )
    return order


def _reduce(g: DAG):
    n = len(g.nodes)
    kept: list[tuple[int, int]] = []

    for u in range(n):
        for v in g.nodes[u].succs:
            if not _reachable_without(g, u, v):
                kept.append((u, v))

    for node in g.nodes:
        node.preds.clear()
        node.succs.clear()
    for u, v in kept:
        g.nodes[u].succs.append(v)
        g.nodes[v].preds.append(u)


def _reachable_without(g: DAG, src: int, dst: int) -> bool:
    visited = set()
    visited.add(src)
    queue: deque[int] = deque()

    for next_node in g.nodes[src].succs:
        if next_node == dst:
            continue
        if next_node not in visited:
            visited.add(next_node)
            queue.append(next_node)

    while queue:
        cur = queue.popleft()
        if cur == dst:
            return True
        for next_node in g.nodes[cur].succs:
            if next_node not in visited:
                visited.add(next_node)
                queue.append(next_node)
    return False
