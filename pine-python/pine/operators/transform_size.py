from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.operator import (
    AbstractOperator,
    ConcurrentSafe,
    ConsumesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class TransformSize(AbstractOperator, ConcurrentSafe, ConsumesRowSet):
    def __init__(self):
        pass

    def init(self, params: OperatorParams):
        pass

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        output.set_common(self.common_output()[0], input_.item_count())
