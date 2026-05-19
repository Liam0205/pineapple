from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.operator import (
    AbstractOperator,
    ConsumesRowSet,
    MutatesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class ReorderSort(AbstractOperator, ConsumesRowSet, MutatesRowSet):
    def __init__(self):
        self._ascending = False

    def init(self, params: OperatorParams):
        order = params.get_string("order", "desc")
        if order == "asc":
            self._ascending = True
        elif order == "desc":
            self._ascending = False
        else:
            raise ValueError(f'reorder_sort: unsupported order "{order}"')

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        n = input_.item_count()
        if n == 0:
            return

        field = self.item_input()[0]
        vals: list[float] = []
        for i in range(n):
            try:
                vals.append(_to_double(input_.item(i, field)))
            except OperatorException as e:
                raise OperatorException(
                    f"reorder_sort: item[{i}].{field}: {e}"
                ) from e

        indices = list(range(n))
        if self._ascending:
            indices.sort(key=lambda idx: vals[idx])
        else:
            indices.sort(key=lambda idx: -vals[idx])

        output.set_item_order(indices)


def _to_double(v) -> float:
    if isinstance(v, bool):
        raise OperatorException("cannot convert bool to float64")
    if isinstance(v, (int, float)):
        return float(v)
    raise OperatorException(
        f"cannot convert {type(v).__name__ if v is not None else 'null'} to double"
    )
