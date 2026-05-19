from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.operator import (
    AbstractOperator,
    ConsumesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class TransformNormalize(AbstractOperator, ConsumesRowSet):
    def __init__(self):
        self._method = "min_max"

    def init(self, params: OperatorParams):
        self._method = params.get_string("method", "min_max")
        if self._method != "min_max":
            raise ValueError(
                f'transform_normalize: unsupported method "{self._method}"'
            )

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        n = input_.item_count()
        if n == 0:
            return

        field = self.item_input()[0]
        output_field = self.item_output()[0]

        vals: list[float] = []
        for i in range(n):
            try:
                vals.append(_to_double(input_.item(i, field)))
            except OperatorException as e:
                raise OperatorException(
                    f"transform_normalize: item[{i}].{field}: {e}"
                ) from e

        min_val = min(vals)
        max_val = max(vals)
        range_val = max_val - min_val

        for i in range(n):
            norm = 0.0 if range_val == 0 else (vals[i] - min_val) / range_val
            output.set_item(i, output_field, norm)


def _to_double(v) -> float:
    if isinstance(v, (int, float)):
        return float(v)
    raise OperatorException(
        f"cannot convert {type(v).__name__ if v is not None else 'null'} to double"
    )
