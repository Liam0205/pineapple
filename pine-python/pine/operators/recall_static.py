from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.operator import (
    AbstractOperator,
    AdditiveWritesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class RecallStatic(AbstractOperator, AdditiveWritesRowSet):
    def __init__(self):
        self._items: list[dict] = []

    def init(self, params: OperatorParams):
        raw = params.get("items")
        if raw is None:
            raise ValueError("recall_static: missing required param 'items'")
        if not isinstance(raw, list):
            raise ValueError(
                f"recall_static: 'items' must be a JSON array, got {type(raw).__name__}"
            )
        for i, item in enumerate(raw):
            if not isinstance(item, dict):
                type_name = type(item).__name__ if item is not None else "null"
                raise ValueError(
                    f"recall_static: items[{i}] must be an object, got {type_name}"
                )
        self._items = [dict(item) for item in raw]

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        for item in self._items:
            output.add_item(dict(item))
