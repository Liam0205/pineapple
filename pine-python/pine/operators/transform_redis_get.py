from __future__ import annotations

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

try:
    import redis as redis_pkg  # type: ignore[import-untyped]
    HAS_REDIS = True
except ImportError:
    HAS_REDIS = False


class TransformRedisGet(AbstractOperator, ConcurrentSafe):
    def __init__(self):
        self._pool: Any = None
        self._key_prefix = ""
        self._data_type = "string"
        self._fail_on_error = False

    def init(self, params: OperatorParams):
        addr = params.get_string("redis_addr", "")
        password = params.get_string("redis_password", "")
        db = params.get_int("redis_db", 0)
        self._key_prefix = params.get_string("key_prefix", "")

        dt = params.get("data_type")
        if isinstance(dt, str) and dt:
            self._data_type = dt

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
        result_field = self.common_output()[0]
        cache_hit_field = self.common_output()[1]

        if self._pool is None:
            output.set_common(cache_hit_field, False)
            return

        key = self._key_prefix + build_key_suffix(input_, self.common_input())

        try:
            client = redis_pkg.Redis(connection_pool=self._pool)

            if self._data_type == "set":
                members = client.smembers(key)
                if members:
                    decoded = [
                        m.decode() if isinstance(m, bytes) else m
                        for m in members
                    ]
                    output.set_common(result_field, decoded)
                    output.set_common(cache_hit_field, True)
                else:
                    output.set_common(cache_hit_field, False)

            elif self._data_type == "list":
                vals = client.lrange(key, 0, -1)
                if vals:
                    decoded = [
                        v.decode() if isinstance(v, bytes) else v
                        for v in vals
                    ]
                    output.set_common(result_field, decoded)
                    output.set_common(cache_hit_field, True)
                else:
                    output.set_common(cache_hit_field, False)

            elif self._data_type == "string":
                val = client.get(key)
                if val is not None and val != b"":
                    output.set_common(result_field, val.decode() if isinstance(val, bytes) else val)
                    output.set_common(cache_hit_field, True)
                else:
                    output.set_common(cache_hit_field, False)

            else:
                raise ValueError(
                    f'transform_redis_get: unsupported data_type "{self._data_type}"'
                )

        except ValueError:
            raise OperatorException(
                f'transform_redis_get: unsupported data_type "{self._data_type}"'
            )
        except Exception as e:
            redis_cmd = {"set": "SMembers", "list": "LRange"}.get(self._data_type, "Get")
            msg = f"transform_redis_get: {redis_cmd}({key}): {e}"
            output.set_warning(OperatorException(msg))
            if self._fail_on_error:
                raise OperatorException(msg) from e
            output.set_common(cache_hit_field, False)


def build_key_suffix(input_: OperatorInput, fields: list[str]) -> str:
    """Build key suffix from common input fields."""
    if not fields:
        return ""
    if len(fields) == 1:
        return _sprint_value(input_.common(fields[0]))
    parts: list[str] = []
    for i, field in enumerate(fields):
        if i > 0:
            parts.append(":")
        parts.append(_sprint_value(input_.common(field)))
    return "".join(parts)


def _sprint_value(v: Any) -> str:
    return go_sprint(v)
