# auto-generated from pine resource schema — DO NOT EDIT
from __future__ import annotations
from typing import Any
from apple.resource import BaseResource


class DatahubProducerResource(BaseResource):
    """Resource: datahub_producer — Benchmark stub: no-op datahub producer."""
    _name = "datahub_producer"
    _default_interval = 0
    _params_schema = {
        "ak_id": {"type": "string", "required": False, "default": ""},
        "ak_secret": {"type": "string", "required": False, "default": ""},
        "endpoint": {"type": "string", "required": False, "default": ""},
        "max_retry": {"type": "int", "required": False, "default": 0},
        "project": {"type": "string", "required": False, "default": ""},
        "topic": {"type": "string", "required": False, "default": ""},
        "user_agent": {"type": "string", "required": False, "default": ""},
    }

    def __init__(
        self,
        *,
        ak_id: str = "",
        ak_secret: str = "",
        endpoint: str = "",
        max_retry: int = 0,
        project: str = "",
        topic: str = "",
        user_agent: str = "",
        interval: int = 0,
    ):
        super().__init__(
            interval=interval,
            ak_id=ak_id,
            ak_secret=ak_secret,
            endpoint=endpoint,
            max_retry=max_retry,
            project=project,
            topic=topic,
            user_agent=user_agent,
        )

class FeedDataResource(BaseResource):
    """Resource: feed_data — Benchmark stub: generates synthetic feed data."""
    _name = "feed_data"
    _default_interval = 0
    _params_schema = {
        "mysql_dsn": {"type": "string", "required": False, "default": ""},
    }

    def __init__(
        self,
        *,
        mysql_dsn: str = "",
        interval: int = 0,
    ):
        super().__init__(
            interval=interval,
            mysql_dsn=mysql_dsn,
        )
