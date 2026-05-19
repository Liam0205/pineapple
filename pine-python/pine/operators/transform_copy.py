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


class TransformCopy(AbstractOperator, ConcurrentSafe, ConsumesRowSet):
    def __init__(self):
        self._direction = ""

    def init(self, params: OperatorParams):
        self._direction = params.get_string("direction")
        if self._direction not in (
            "common_to_item", "item_to_common", "common_to_common", "item_to_item"
        ):
            raise ValueError(
                f'transform_copy: unsupported direction "{self._direction}"'
            )

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        if self._direction == "common_to_common":
            for i in range(len(self.common_input())):
                output.set_common(
                    self.common_output()[i],
                    input_.common(self.common_input()[i]),
                )

        elif self._direction == "common_to_item":
            for i in range(len(self.common_input())):
                val = input_.common(self.common_input()[i])
                dst = self.item_output()[i]
                for j in range(input_.item_count()):
                    output.set_item(j, dst, val)

        elif self._direction == "item_to_item":
            for i in range(len(self.item_input())):
                src = self.item_input()[i]
                dst = self.item_output()[i]
                for j in range(input_.item_count()):
                    output.set_item(j, dst, input_.item(j, src))

        elif self._direction == "item_to_common":
            for i in range(len(self.item_input())):
                src = self.item_input()[i]
                vals: list = []
                for j in range(input_.item_count()):
                    vals.append(input_.item(j, src))
                output.set_common(self.common_output()[i], vals)
