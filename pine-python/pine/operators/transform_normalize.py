from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.operator import (
    AbstractOperator,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class TransformNormalize(AbstractOperator):
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


def _go_type_name(v) -> str:
    """Render a Python value's type using Go's reflection terminology so error
    messages stay byte-identical with pine-{go,cpp,java}."""
    if v is None:
        return "<nil>"
    if isinstance(v, bool):
        return "bool"
    if isinstance(v, str):
        return "string"
    if isinstance(v, int):
        return "int"
    if isinstance(v, float):
        return "float64"
    if isinstance(v, list):
        return "[]interface {}"
    if isinstance(v, dict):
        return "map[string]interface {}"
    return type(v).__name__


def _to_double(v) -> float:
    if isinstance(v, bool):
        raise OperatorException("cannot convert bool to float64")
    if isinstance(v, (int, float)):
        return float(v)
    raise OperatorException(
        f"cannot convert {_go_type_name(v)} to float64"
    )
