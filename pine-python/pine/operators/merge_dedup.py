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
    # V-9: stringify composite types (dict/list) that are unhashable in
    # Python sets. Matches Go fmt.Sprint / Java GoFormat.sprint / C++
    # dedup_key string path. Scalar types (str/int/float/bool/None) are
    # hashable and stay as-is for efficient set lookup.
    if isinstance(v, (dict, list)):
        import json
        return json.dumps(v, sort_keys=True, ensure_ascii=False)
    if isinstance(v, float) and v == 0.0:
        return 0.0  # canonicalize -0.0 to +0.0
    return v
