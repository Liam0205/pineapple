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


class FilterTruncate(AbstractOperator, ConsumesRowSet, MutatesRowSet):
    def __init__(self):
        self._top_n = 0

    def init(self, params: OperatorParams):
        v = params.get("top_n")
        if not isinstance(v, (int, float)):
            raise ValueError(
                f"filter_truncate: top_n must be numeric, got "
                f"{type(v).__name__ if v is not None else 'null'}"
            )
        self._top_n = int(v)
        if self._top_n < 0:
            raise ValueError(f"filter_truncate: top_n must be non-negative, got {self._top_n}")

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        start = min(self._top_n, input_.item_count())
        for i in range(start, input_.item_count()):
            output.remove_item(i)
