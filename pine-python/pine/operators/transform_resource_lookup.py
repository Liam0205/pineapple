from __future__ import annotations

from typing import Any

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.go_format import format_float_f
from pine.operator import (
    AbstractOperator,
    ConcurrentSafe,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
    ResourceAware,
)


class TransformResourceLookup(AbstractOperator, ConcurrentSafe, ResourceAware):
    def __init__(self):
        self._resource_name = ""
        self._lookup_key = ""
        self._output_field = ""
        self._default_value: Any = None
        self._has_default = False
        self._resource_provider: Any = None

    def init(self, params: OperatorParams):
        self._resource_name = params.get_string("resource_name")
        self._lookup_key = params.get_string("lookup_key")
        self._output_field = params.get_string("output_field")
        if params.contains_key("default_value"):
            self._default_value = params.get("default_value")
            self._has_default = True

    def set_resource_provider(self, provider: Any):
        self._resource_provider = provider

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        if self._resource_provider is None:
            raise OperatorException(
                "transform_resource_lookup: no resource provider in context"
            )

        result = self._resource_provider.get(self._resource_name)
        if not result.exists():
            raise OperatorException(
                f'transform_resource_lookup: resource "{self._resource_name}" not found'
            )

        raw = result.value()
        if not isinstance(raw, dict):
            type_name = type(raw).__name__ if raw is not None else "null"
            raise OperatorException(
                f'transform_resource_lookup: resource "{self._resource_name}" is '
                f"{type_name}, want dict[str, Any]"
            )

        table: dict[str, Any] = raw

        for i in range(input_.item_count()):
            key_raw = input_.item(i, self._lookup_key)
            if key_raw is None:
                if self._has_default:
                    output.set_item(i, self._output_field, self._default_value)
                continue

            key = _to_key_string(key_raw)
            if key in table:
                output.set_item(i, self._output_field, table[key])
            elif self._has_default:
                output.set_item(i, self._output_field, self._default_value)


def _to_key_string(v: Any) -> str:
    """Convert a value to its string key representation for lookup."""
    if isinstance(v, str):
        return v
    if isinstance(v, (int, float)):
        d = float(v)
        if d == int(d):
            return str(int(d))
        return format_float_f(d)
    return str(v)
