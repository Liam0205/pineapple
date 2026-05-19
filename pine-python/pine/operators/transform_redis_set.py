from __future__ import annotations

import sys
from typing import Any

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.go_format import sprint as go_sprint
from pine.operator import (
    AbstractOperator,
    ConcurrentSafe,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)
from pine.operators.transform_redis_get import build_key_suffix

try:
    import redis as redis_pkg  # type: ignore[import-untyped]
    HAS_REDIS = True
except ImportError:
    HAS_REDIS = False


class TransformRedisSet(AbstractOperator, ConcurrentSafe):
    def __init__(self):
        self._pool: Any = None
        self._key_prefix = ""
        self._data_type = "string"
        self._ttl_seconds = 0
        self._fail_on_error = False

    def init(self, params: OperatorParams):
        addr = params.get_string("redis_addr", "")
        password = params.get_string("redis_password", "")
        db = params.get_int("redis_db", 0)
        self._key_prefix = params.get_string("key_prefix", "")

        dt = params.get("data_type")
        if isinstance(dt, str) and dt:
            self._data_type = dt

        self._ttl_seconds = params.get_int("ttl", 0)

        foe = params.get("fail_on_error")
        if isinstance(foe, bool):
            self._fail_on_error = foe

        if addr and HAS_REDIS:
            if ":" in addr:
                host, port_str = addr.rsplit(":", 1)
                port = int(port_str)
            else:
                host = addr
                port = 6379
            self._pool = redis_pkg.ConnectionPool(
                host=host, port=port, db=db,
                password=password if password else None,
                socket_timeout=2.0,
            )

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        if self._pool is None:
            return

        n = len(self.common_input())
        if n < 2:
            raise OperatorException(
                "transform_redis_set: common_input must have at least 2 fields "
                "(key fields + value field)"
            )

        key = self._key_prefix + build_key_suffix(input_, self.common_input()[: n - 1])
        value = input_.common(self.common_input()[n - 1])

        try:
            client = redis_pkg.Redis(connection_pool=self._pool)

            if self._data_type == "set":
                members = _to_string_list(value)
                if members is None:
                    print(
                        f"transform_redis_set: value for key {key} is not []string",
                        file=sys.stderr,
                    )
                    return
                if not members:
                    return
                pipe = client.pipeline()
                pipe.delete(key)
                pipe.sadd(key, *members)
                if self._ttl_seconds > 0:
                    pipe.expire(key, self._ttl_seconds)
                pipe.execute()

            elif self._data_type == "list":
                members = _to_string_list(value)
                if members is None:
                    print(
                        f"transform_redis_set: value for key {key} is not []string",
                        file=sys.stderr,
                    )
                    return
                if not members:
                    return
                pipe = client.pipeline()
                pipe.delete(key)
                pipe.rpush(key, *members)
                if self._ttl_seconds > 0:
                    pipe.expire(key, self._ttl_seconds)
                pipe.execute()

            elif self._data_type == "string":
                if not isinstance(value, str):
                    print(
                        f"transform_redis_set: value for key {key} is not string",
                        file=sys.stderr,
                    )
                    return
                if self._ttl_seconds > 0:
                    client.setex(key, self._ttl_seconds, value)
                else:
                    client.set(key, value)

            else:
                raise ValueError(
                    f'transform_redis_set: unsupported data_type "{self._data_type}"'
                )

        except ValueError as e:
            raise OperatorException(str(e)) from e
        except Exception as e:
            msg = f"transform_redis_set: write key {key}: {e}"
            if self._fail_on_error:
                raise OperatorException(msg) from e
            print(msg, file=sys.stderr)
            output.set_warning(OperatorException(msg))


def _to_string_list(v: Any) -> list[str] | None:
    """Convert value to list of strings. Returns None if not a list."""
    if isinstance(v, list):
        return [go_sprint(item) for item in v]
    return None
