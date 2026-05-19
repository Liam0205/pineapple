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


class FilterPaginate(AbstractOperator, ConsumesRowSet, MutatesRowSet):
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

        page = _to_int(input_.common(self.common_input()[0]))
        size = _to_int(input_.common(self.common_input()[1]))
        if size <= 0:
            size = 10
        if page < 0:
            page = 0

        start = page * size
        end = min(start + size, n)

        for i in range(n):
            if i < start or i >= end:
                output.remove_item(i)


def _to_int(v) -> int:
    if isinstance(v, (int, float)):
        return int(v)
    from pine.errors import OperatorException
    raise OperatorException(f"filter_paginate: expected numeric value, got {type(v).__name__}")
