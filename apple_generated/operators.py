# auto-generated from pine operator schema — DO NOT EDIT
from __future__ import annotations
from typing import Any
from apple.base import BaseOp


class FilterConditionOp(BaseOp):
    """Operator: filter_condition"""
    _name = "filter_condition"
    _params_schema = {
        "value": {"type": "any", "required": True},
    }

    def __call__(
        self,
        *,
        value: Any = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "FilterConditionOp":
        return self._apply(
            params={
                "value": value,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )

class FilterTruncateOp(BaseOp):
    """Operator: filter_truncate"""
    _name = "filter_truncate"
    _params_schema = {
        "top_n": {"type": "int64", "required": True},
    }

    def __call__(
        self,
        *,
        top_n: int = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "FilterTruncateOp":
        return self._apply(
            params={
                "top_n": top_n,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )

class MergeDedupOp(BaseOp):
    """Operator: merge_dedup"""
    _name = "merge_dedup"
    _params_schema = {
        "strategy": {"type": "string", "required": False, "default": "first"},
    }

    def __call__(
        self,
        *,
        strategy: str = "",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "MergeDedupOp":
        return self._apply(
            params={
                "strategy": strategy,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )

class ObserveLogOp(BaseOp):
    """Operator: observe_log"""
    _name = "observe_log"
    _params_schema = {
        "log_prefix": {"type": "string", "required": False},
    }

    def __call__(
        self,
        *,
        log_prefix: str = "",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "ObserveLogOp":
        return self._apply(
            params={
                "log_prefix": log_prefix,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )

class RecallStaticOp(BaseOp):
    """Operator: recall_static"""
    _name = "recall_static"
    _params_schema = {
        "items": {"type": "any", "required": True},
    }

    def __call__(
        self,
        *,
        items: Any = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "RecallStaticOp":
        return self._apply(
            params={
                "items": items,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            recall=True,
            debug=debug,
            name=name or "",
        )

class ReorderSortOp(BaseOp):
    """Operator: reorder_sort"""
    _name = "reorder_sort"
    _params_schema = {
        "order": {"type": "string", "required": False, "default": "desc"},
    }

    def __call__(
        self,
        *,
        order: str = "",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "ReorderSortOp":
        return self._apply(
            params={
                "order": order,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )

class TransformByLuaOp(BaseOp):
    """Operator: transform_by_lua"""
    _name = "transform_by_lua"
    _params_schema = {
        "function_for_common": {"type": "string", "required": False},
        "function_for_item": {"type": "string", "required": False},
        "lua_script": {"type": "string", "required": True},
    }

    def __call__(
        self,
        *,
        function_for_common: str = "",
        function_for_item: str = "",
        lua_script: str = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformByLuaOp":
        return self._apply(
            params={
                "function_for_common": function_for_common,
                "function_for_item": function_for_item,
                "lua_script": lua_script,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )

class TransformDispatchOp(BaseOp):
    """Operator: transform_dispatch"""
    _name = "transform_dispatch"
    _params_schema = {
    }

    def __call__(
        self,
        *,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformDispatchOp":
        return self._apply(
            params={
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )

class TransformNormalizeOp(BaseOp):
    """Operator: transform_normalize"""
    _name = "transform_normalize"
    _params_schema = {
        "method": {"type": "string", "required": False, "default": "min_max"},
    }

    def __call__(
        self,
        *,
        method: str = "",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformNormalizeOp":
        return self._apply(
            params={
                "method": method,
            },
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            debug=debug,
            name=name or "",
        )
