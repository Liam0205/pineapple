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


def _go_type_name(value):
    """Render a Python value's type using Go's reflection terminology so error
    messages stay byte-identical with pine-{go,cpp,java}."""
    if value is None:
        return "<nil>"
    if isinstance(value, bool):
        return "bool"
    if isinstance(value, str):
        return "string"
    if isinstance(value, int):
        return "int"
    if isinstance(value, float):
        return "float64"
    if isinstance(value, list):
        return "[]interface {}"
    if isinstance(value, dict):
        return "map[string]interface {}"
    return type(value).__name__


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
            raise OperatorException(
                f'recall_resource: resource "{self._resource_name}" is {_go_type_name(raw)}, '
                f"want []map[string]any"
            )

        for i, item in enumerate(raw):
            if not isinstance(item, dict):
                raise OperatorException(
                    f"recall_resource: items[{i}] is {_go_type_name(item)}, want map[string]any"
                )
            output.add_item(dict(item))
