# auto-generated from pine resource schema — DO NOT EDIT
# ruff: noqa: E501
from __future__ import annotations

from apple.resource import BaseResource


class RedisConnectionResource(BaseResource):
    """Resource: redis_connection — Shared Redis connection pool borrowed by Redis operators via resource_name."""
    _name = "redis_connection"
    _default_interval = -1
    _params_schema = {
        "addr": {"type": "string", "required": True},
        "db": {"type": "int", "required": False, "default": 0},
        "dial_timeout_ms": {"type": "int", "required": False, "default": 2000},
        "metrics_name": {"type": "string", "required": False, "default": ""},
        "password": {"type": "string", "required": False, "default": ""},
        "pool_size": {"type": "int", "required": False, "default": 0},
        "pool_timeout_ms": {"type": "int", "required": False, "default": 2000},
        "read_timeout_ms": {"type": "int", "required": False, "default": 2000},
        "write_timeout_ms": {"type": "int", "required": False, "default": 2000},
    }

    def __init__(
        self,
        *,
        addr: str = ...,
        db: int = 0,
        dial_timeout_ms: int = 2000,
        metrics_name: str = "",
        password: str = "",
        pool_size: int = 0,
        pool_timeout_ms: int = 2000,
        read_timeout_ms: int = 2000,
        write_timeout_ms: int = 2000,
        interval: int = -1,
    ):
        super().__init__(
            interval=interval,
            addr=addr,
            db=db,
            dial_timeout_ms=dial_timeout_ms,
            metrics_name=metrics_name,
            password=password,
            pool_size=pool_size,
            pool_timeout_ms=pool_timeout_ms,
            read_timeout_ms=read_timeout_ms,
            write_timeout_ms=write_timeout_ms,
        )
