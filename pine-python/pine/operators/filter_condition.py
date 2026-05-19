from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.go_format import sprint as go_sprint
from pine.operator import (
    AbstractOperator,
    ConsumesRowSet,
    MutatesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class FilterCondition(AbstractOperator, ConsumesRowSet, MutatesRowSet):
    def __init__(self):
        self._value = None

    def init(self, params: OperatorParams):
        self._value = params.get("value")

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        field = self.item_input()[0]
        for i in range(input_.item_count()):
            if go_sprint(input_.item(i, field)) == go_sprint(self._value):
                output.remove_item(i)
