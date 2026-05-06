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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "FilterConditionOp":
        _params = {
            "value": value,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class FilterPaginateOp(BaseOp):
    """Operator: filter_paginate"""
    _name = "filter_paginate"
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "FilterPaginateOp":
        _params = {
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "FilterTruncateOp":
        _params = {
            "top_n": top_n,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
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
        strategy: str = "first",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "MergeDedupOp":
        _params = {
            "strategy": strategy,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class ObserveLogOp(BaseOp):
    """Operator: observe_log"""
    _name = "observe_log"
    _params_schema = {
        "log_prefix": {"type": "string", "required": False, "default": ""},
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "ObserveLogOp":
        _params = {
            "log_prefix": log_prefix,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class RecallResourceOp(BaseOp):
    """Operator: recall_resource"""
    _name = "recall_resource"
    _params_schema = {
        "resource_name": {"type": "string", "required": True},
    }

    def __call__(
        self,
        *,
        resource_name: str = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "RecallResourceOp":
        _params = {
            "resource_name": resource_name,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            recall=True,
            row_dependency=row_dependency,
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "RecallStaticOp":
        _params = {
            "items": items,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            recall=True,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class ReorderShuffleBySaltOp(BaseOp):
    """Operator: reorder_shuffle_by_salt"""
    _name = "reorder_shuffle_by_salt"
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "ReorderShuffleBySaltOp":
        _params = {
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
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
        order: str = "desc",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "ReorderSortOp":
        _params = {
            "order": order,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class TransformByLuaOp(BaseOp):
    """Operator: transform_by_lua"""
    _name = "transform_by_lua"
    _params_schema = {
        "function_for_common": {"type": "string", "required": False, "default": ""},
        "function_for_item": {"type": "string", "required": False, "default": ""},
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformByLuaOp":
        _params = {
            "function_for_common": function_for_common,
            "function_for_item": function_for_item,
            "lua_script": lua_script,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class TransformByRemotePineappleOp(BaseOp):
    """Operator: transform_by_remote_pineapple"""
    _name = "transform_by_remote_pineapple"
    _params_schema = {
        "allow_private": {"type": "bool", "required": False, "default": False},
        "common_request": {"type": "any", "required": False},
        "common_response": {"type": "any", "required": False},
        "endpoint": {"type": "string", "required": False, "default": "/execute"},
        "fail_on_error": {"type": "bool", "required": False, "default": True},
        "host": {"type": "string", "required": True},
        "item_request": {"type": "any", "required": False},
        "item_response": {"type": "any", "required": False},
        "max_response_size": {"type": "int64", "required": False, "default": 10485760},
        "port": {"type": "int64", "required": True},
        "timeout": {"type": "float64", "required": False, "default": 5},
    }

    def __call__(
        self,
        *,
        allow_private: bool = False,
        common_request: Any = None,
        common_response: Any = None,
        endpoint: str = "/execute",
        fail_on_error: bool = True,
        host: str = ...,
        item_request: Any = None,
        item_response: Any = None,
        max_response_size: int = 10485760,
        port: int = ...,
        timeout: float = 5,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformByRemotePineappleOp":
        _params = {
            "allow_private": allow_private,
            "endpoint": endpoint,
            "fail_on_error": fail_on_error,
            "host": host,
            "max_response_size": max_response_size,
            "port": port,
            "timeout": timeout,
        }
        if common_request is not None:
            _params["common_request"] = common_request
        if common_response is not None:
            _params["common_response"] = common_response
        if item_request is not None:
            _params["item_request"] = item_request
        if item_response is not None:
            _params["item_response"] = item_response
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class TransformCopyOp(BaseOp):
    """Operator: transform_copy"""
    _name = "transform_copy"
    _params_schema = {
        "direction": {"type": "string", "required": True},
    }

    def __call__(
        self,
        *,
        direction: str = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformCopyOp":
        _params = {
            "direction": direction,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformDispatchOp":
        _params = {
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
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
        method: str = "min_max",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformNormalizeOp":
        _params = {
            "method": method,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class TransformRedisGetOp(BaseOp):
    """Operator: transform_redis_get"""
    _name = "transform_redis_get"
    _params_schema = {
        "data_type": {"type": "string", "required": False, "default": "string"},
        "fail_on_error": {"type": "bool", "required": False, "default": False},
        "key_prefix": {"type": "string", "required": True},
        "redis_addr": {"type": "string", "required": True},
        "redis_db": {"type": "int", "required": False, "default": 0},
        "redis_password": {"type": "string", "required": False, "default": ""},
    }

    def __call__(
        self,
        *,
        data_type: str = "string",
        fail_on_error: bool = False,
        key_prefix: str = ...,
        redis_addr: str = ...,
        redis_db: int = 0,
        redis_password: str = "",
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformRedisGetOp":
        _params = {
            "data_type": data_type,
            "fail_on_error": fail_on_error,
            "key_prefix": key_prefix,
            "redis_addr": redis_addr,
            "redis_db": redis_db,
            "redis_password": redis_password,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class TransformRedisSetOp(BaseOp):
    """Operator: transform_redis_set"""
    _name = "transform_redis_set"
    _params_schema = {
        "data_type": {"type": "string", "required": False, "default": "string"},
        "key_prefix": {"type": "string", "required": True},
        "redis_addr": {"type": "string", "required": True},
        "redis_db": {"type": "int", "required": False, "default": 0},
        "redis_password": {"type": "string", "required": False, "default": ""},
        "ttl": {"type": "int", "required": False, "default": 0},
    }

    def __call__(
        self,
        *,
        data_type: str = "string",
        key_prefix: str = ...,
        redis_addr: str = ...,
        redis_db: int = 0,
        redis_password: str = "",
        ttl: int = 0,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformRedisSetOp":
        _params = {
            "data_type": data_type,
            "key_prefix": key_prefix,
            "redis_addr": redis_addr,
            "redis_db": redis_db,
            "redis_password": redis_password,
            "ttl": ttl,
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class TransformResourceLookupOp(BaseOp):
    """Operator: transform_resource_lookup"""
    _name = "transform_resource_lookup"
    _params_schema = {
        "default_value": {"type": "any", "required": False},
        "lookup_key": {"type": "string", "required": True},
        "output_field": {"type": "string", "required": True},
        "resource_name": {"type": "string", "required": True},
    }

    def __call__(
        self,
        *,
        default_value: Any = None,
        lookup_key: str = ...,
        output_field: str = ...,
        resource_name: str = ...,
        common_input: list[str] | None = None,
        common_output: list[str] | None = None,
        item_input: list[str] | None = None,
        item_output: list[str] | None = None,
        item_defaults: dict | None = None,
        common_defaults: dict | None = None,
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformResourceLookupOp":
        _params = {
            "lookup_key": lookup_key,
            "output_field": output_field,
            "resource_name": resource_name,
        }
        if default_value is not None:
            _params["default_value"] = default_value
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )

class TransformSizeOp(BaseOp):
    """Operator: transform_size"""
    _name = "transform_size"
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
        row_dependency: bool = False,
        debug: bool = False,
        name: str | None = None,
    ) -> "TransformSizeOp":
        _params = {
        }
        return self._apply(
            params=_params,
            common_input=common_input,
            common_output=common_output,
            item_input=item_input,
            item_output=item_output,
            item_defaults=item_defaults,
            common_defaults=common_defaults,
            row_dependency=row_dependency,
            debug=debug,
            name=name or "",
        )
