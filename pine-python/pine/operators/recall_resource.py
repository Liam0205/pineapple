from __future__ import annotations

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.operator import (
    AbstractOperator,
    AdditiveWritesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
    ResourceAware,
)


class RecallResource(AbstractOperator, AdditiveWritesRowSet, ResourceAware):
    def __init__(self):
        self._resource_name = ""
        self._resource_provider = None

    def init(self, params: OperatorParams):
        self._resource_name = params.get_string("resource_name")

    def set_resource_provider(self, provider):
        self._resource_provider = provider

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        if self._resource_provider is None:
            raise OperatorException("recall_resource: no resource provider in context")

        result = self._resource_provider.get(self._resource_name)
        if not result.exists():
            raise OperatorException(
                f'recall_resource: resource "{self._resource_name}" not found'
            )

        raw = result.value()
        if not isinstance(raw, list):
            type_name = type(raw).__name__ if raw is not None else "null"
            raise OperatorException(
                f'recall_resource: resource "{self._resource_name}" is {type_name}, '
                f"want list[dict[str, Any]]"
            )

        for i, item in enumerate(raw):
            if not isinstance(item, dict):
                type_name = type(item).__name__ if item is not None else "null"
                raise OperatorException(
                    f"recall_resource: items[{i}] is {type_name}, want dict"
                )
            output.add_item(dict(item))
