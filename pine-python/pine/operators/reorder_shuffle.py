from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.go_format import format_g
from pine.operator import (
    AbstractOperator,
    ConsumesRowSet,
    MutatesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class ReorderShuffle(AbstractOperator, ConsumesRowSet, MutatesRowSet):
    def __init__(self):
        pass

    def init(self, params: OperatorParams):
        pass

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        n = input_.item_count()
        if n == 0:
            return

        parts: list[str] = []
        for i, field in enumerate(self.common_input()):
            if i > 0:
                parts.append("|")
            parts.append(_any_to_string(input_.common(field)))
        parts.append("|")
        salt_prefix = "".join(parts)

        item_field = self.item_input()[0]
        ranks: list[float] = []
        ids: list[int] = []
        indices = list(range(n))

        for i in range(n):
            item_val = _any_to_string(input_.item(i, item_field))
            key = salt_prefix + item_val
            ranks.append(_hash_to_unit_interval(key))
            ids.append(_parse_uint64(item_val))

        indices.sort(key=lambda idx: (ranks[idx], ids[idx]))
        output.set_item_order(indices)


def _hash_to_unit_interval(s: str) -> float:
    h = _fnv64a(s)
    # Convert signed 64-bit to unsigned
    unsigned = h & 0xFFFFFFFFFFFFFFFF
    return unsigned / 18446744073709551616.0


def _fnv64a(s: str) -> int:
    data = s.encode("utf-8")
    hash_val = 0xCBF29CE484222325
    mask = 0xFFFFFFFFFFFFFFFF
    for b in data:
        hash_val ^= b
        hash_val = (hash_val * 0x100000001B3) & mask
    # Return as signed 64-bit for consistency
    if hash_val >= 0x8000000000000000:
        hash_val -= 0x10000000000000000
    return hash_val


def _any_to_string(v) -> str:
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, int):
        return str(v)
    if isinstance(v, float):
        return format_g(v)
    return str(v)


def _parse_uint64(s: str) -> int:
    try:
        val = int(s)
        if val < 0:
            return 0
        return val & 0xFFFFFFFFFFFFFFFF
    except (ValueError, TypeError):
        return 0
