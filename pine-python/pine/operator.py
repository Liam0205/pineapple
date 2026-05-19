from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from enum import Enum
from typing import Any

from pine.cancellation import CancellationToken


class OperatorParams:
    """Type-safe accessor for operator configuration parameters."""

    def __init__(self, params: dict[str, Any]):
        self._params = params

    def get(self, key: str) -> Any:
        return self._params.get(key)

    def get_string(self, key: str, default: str = "") -> str:
        v = self._params.get(key)
        if v is None:
            return default
        return str(v)

    def get_int(self, key: str, default: int = 0) -> int:
        v = self._params.get(key)
        if v is None:
            return default
        if isinstance(v, (int, float)):
            return int(v)
        return default

    def get_float(self, key: str, default: float = 0.0) -> float:
        v = self._params.get(key)
        if v is None:
            return default
        if isinstance(v, (int, float)):
            return float(v)
        return default

    def get_bool(self, key: str, default: bool = False) -> bool:
        v = self._params.get(key)
        if v is None:
            return default
        if isinstance(v, bool):
            return v
        return default

    def get_string_list(self, key: str) -> list[str]:
        v = self._params.get(key)
        if v is None:
            return []
        if isinstance(v, list):
            return [str(x) for x in v]
        return []

    def contains_key(self, key: str) -> bool:
        return key in self._params

    def keys(self) -> set[str]:
        return set(self._params.keys())

    def to_map(self) -> dict[str, Any]:
        return dict(self._params)


class OperatorInput:
    """Snapshot of common fields and item rows provided to an operator."""

    def __init__(self, common: dict[str, Any], items: list[dict[str, Any]]):
        self._common = common if common is not None else {}
        self._items = items if items is not None else []

    def common(self, field_name: str) -> Any:
        return self._common.get(field_name)

    def item_count(self) -> int:
        return len(self._items)

    def item(self, index: int, field_name: str) -> Any:
        if index < 0 or index >= len(self._items):
            return None
        return self._items[index].get(field_name)

    def raw_common(self) -> dict[str, Any]:
        return self._common

    def raw_items(self) -> list[dict[str, Any]]:
        return self._items


class OperatorOutput:
    """Accumulates an operator's writes, additions, removals, and reorderings."""

    def __init__(self):
        self._common_writes: dict[str, Any] = {}
        self._item_writes: dict[int, dict[str, Any]] = {}
        self._added_items: list[dict[str, Any]] = []
        self._removed_items: set[int] = set()
        self._item_order: list[int] | None = None
        self._warning: Exception | None = None

    def set_warning(self, w: Exception):
        if self._warning is None:
            self._warning = w

    @property
    def warning(self) -> Exception | None:
        return self._warning

    def set_common(self, field_name: str, value: Any):
        self._common_writes[field_name] = value

    def set_item(self, index: int, field_name: str, value: Any):
        if index not in self._item_writes:
            self._item_writes[index] = {}
        self._item_writes[index][field_name] = value

    def add_item(self, fields: dict[str, Any]):
        self._added_items.append(fields)

    def remove_item(self, index: int):
        self._removed_items.add(index)

    def set_item_order(self, order: list[int]):
        self._item_order = order

    @property
    def common_writes(self) -> dict[str, Any]:
        return self._common_writes

    @property
    def item_writes(self) -> dict[int, dict[str, Any]]:
        return self._item_writes

    @property
    def added_items(self) -> list[dict[str, Any]]:
        return self._added_items

    @property
    def removed_items(self) -> set[int]:
        return self._removed_items

    @property
    def item_order(self) -> list[int] | None:
        return self._item_order


class OperatorType(Enum):
    """Enum classifying operator capabilities and output constraints."""

    RECALL = "recall"
    TRANSFORM = "transform"
    FILTER = "filter"
    MERGE = "merge"
    REORDER = "reorder"
    OBSERVE = "observe"

    def validate_output(self, out: OperatorOutput) -> str | None:
        violations: list[str] = []
        has_common_writes = bool(out.common_writes)
        has_item_writes = bool(out.item_writes)
        has_added_items = bool(out.added_items)
        has_removed_items = bool(out.removed_items)
        has_item_order = out.item_order is not None

        match self:
            case OperatorType.RECALL:
                if has_common_writes:
                    violations.append("SetCommon")
                if has_item_writes:
                    violations.append("SetItem")
                if has_removed_items:
                    violations.append("RemoveItem")
                if has_item_order:
                    violations.append("SetItemOrder")
            case OperatorType.TRANSFORM:
                if has_added_items:
                    violations.append("AddItem")
                if has_removed_items:
                    violations.append("RemoveItem")
                if has_item_order:
                    violations.append("SetItemOrder")
            case OperatorType.FILTER:
                if has_common_writes:
                    violations.append("SetCommon")
                if has_item_writes:
                    violations.append("SetItem")
                if has_added_items:
                    violations.append("AddItem")
                if has_item_order:
                    violations.append("SetItemOrder")
            case OperatorType.MERGE:
                if has_common_writes:
                    violations.append("SetCommon")
                if has_added_items:
                    violations.append("AddItem")
                if has_item_order:
                    violations.append("SetItemOrder")
            case OperatorType.REORDER:
                if has_common_writes:
                    violations.append("SetCommon")
                if has_item_writes:
                    violations.append("SetItem")
                if has_added_items:
                    violations.append("AddItem")
                if has_removed_items:
                    violations.append("RemoveItem")
            case OperatorType.OBSERVE:
                if has_common_writes:
                    violations.append("SetCommon")
                if has_item_writes:
                    violations.append("SetItem")
                if has_added_items:
                    violations.append("AddItem")
                if has_removed_items:
                    violations.append("RemoveItem")
                if has_item_order:
                    violations.append("SetItemOrder")

        if not violations:
            return None
        type_name = self.name.capitalize()
        methods = " ".join(violations)
        return f"operator type {type_name} must not call [{methods}]"


@dataclass
class ParamSpec:
    """Schema for a single operator parameter."""

    type: str
    required: bool
    default_value: Any = None
    description: str = ""

    @classmethod
    def required_param(cls, type_: str, description: str) -> "ParamSpec":
        return cls(type=type_, required=True, description=description)

    @classmethod
    def optional(cls, type_: str, default: Any, description: str) -> "ParamSpec":
        return cls(type=type_, required=False, default_value=default, description=description)


@dataclass
class OperatorSchema:
    """Declares an operator's name, type, description, and parameter specs."""

    name: str
    type: OperatorType
    description: str
    params: dict[str, ParamSpec] = field(default_factory=dict)


class Operator(ABC):
    """Abstract base for all pipeline operators."""

    @abstractmethod
    def init(self, params: OperatorParams):
        ...

    @abstractmethod
    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        ...


class MetadataAware:
    _common_input: list[str] = []
    _common_output: list[str] = []
    _item_input: list[str] = []
    _item_output: list[str] = []

    def set_metadata(self, common_input: list[str], common_output: list[str],
                     item_input: list[str], item_output: list[str]):
        self._common_input = common_input
        self._common_output = common_output
        self._item_input = item_input
        self._item_output = item_output

    def common_input(self) -> list[str]:
        return self._common_input

    def common_output(self) -> list[str]:
        return self._common_output

    def item_input(self) -> list[str]:
        return self._item_input

    def item_output(self) -> list[str]:
        return self._item_output


class DebugAware:
    _debug_name: str = ""
    _debug_enabled: bool = False

    def set_debug(self, name: str, enabled: bool):
        self._debug_name = name
        self._debug_enabled = enabled


class MetricsAware:
    _metrics_provider: Any = None

    def set_metrics_provider(self, provider: Any):
        self._metrics_provider = provider


class ResourceAware:
    _resource_provider: Any = None

    def set_resource_provider(self, provider: Any):
        self._resource_provider = provider


class StatsProvider:
    def operator_stats(self) -> dict[str, int]:
        return {}


class ConcurrentSafe:
    pass


class ConsumesRowSet:
    """Marker: operator iterates items and needs the row set stable before execution."""
    pass


class MutatesRowSet:
    """Marker: operator changes which items exist or their order (remove/reorder)."""
    pass


class AdditiveWritesRowSet:
    """Marker: operator appends new items without reading or modifying existing ones."""
    pass


class AbstractOperator(Operator, MetadataAware):
    pass
