from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.operator import (
    AbstractOperator,
    ConcurrentSafe,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class TransformDispatch(AbstractOperator, ConcurrentSafe):
    def __init__(self):
        pass

    def init(self, params: OperatorParams):
        pass

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        common_field = self.common_input()[0]
        item_field = self.item_output()[0]
        val = input_.common(common_field)
        for i in range(input_.item_count()):
            output.set_item(i, item_field, val)
