from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.operator import (
    AbstractOperator,
    ConsumesRowSet,
    MutatesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class MergeDedup(AbstractOperator, ConsumesRowSet, MutatesRowSet):
    def __init__(self):
        self._strategy = "first"

    def init(self, params: OperatorParams):
        self._strategy = params.get_string("strategy", "first")
        if self._strategy != "first":
            raise ValueError(f'merge_dedup: unsupported strategy "{self._strategy}"')

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        dedup_by = self.item_input()[0]
        seen: set = set()
        for i in range(input_.item_count()):
            key = _normalize_key(input_.item(i, dedup_by))
            if key in seen:
                output.remove_item(i)
            else:
                seen.add(key)


def _normalize_key(v):
    # All types must produce distinct, hashable keys.
    # Python's set treats False==0==0.0 and True==1 as equal (PEP 285),
    # so we must stringify with type prefix to avoid cross-type collision.
    # Matches Go fmt.Sprintf("%T:%v") and C++ dedup_key "B:"/"F:"/"S:"/"N:" prefixes.
    if v is None:
        return "N:"
    if isinstance(v, bool):
        return "B:1" if v else "B:0"
    if isinstance(v, (int, float)):
        d = float(v)
        if d == 0.0:
            d = 0.0  # canonicalize -0.0
        from pine.go_format import format_g
        return "F:" + format_g(d)
    if isinstance(v, str):
        return "S:" + v
    if isinstance(v, (dict, list)):
        import json
        return "O:" + json.dumps(v, sort_keys=True, ensure_ascii=False)
    return "O:" + str(v)
