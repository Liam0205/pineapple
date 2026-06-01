# auto-generated from pine resource schema — DO NOT EDIT
from __future__ import annotations
from typing import Any
from apple.resource import BaseResource


class RedisConnectionResource(BaseResource):
    """Resource: redis_connection — Shared Redis connection pool borrowed by Redis operators via resource_name."""
    _name = "redis_connection"
    _default_interval = -1
    _params_schema = {
        "addr": {"type": "string", "required": True},
        "db": {"type": "int", "required": False, "default": 0},
        "metrics_name": {"type": "string", "required": False, "default": ""},
        "password": {"type": "string", "required": False, "default": ""},
    }

    def __init__(
        self,
        *,
        addr: str = ...,
        db: int = 0,
        metrics_name: str = "",
        password: str = "",
        interval: int = -1,
    ):
        super().__init__(
            interval=interval,
            addr=addr,
            db=db,
            metrics_name=metrics_name,
            password=password,
        )
