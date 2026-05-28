from __future__ import annotations

import sys
import threading
from collections import deque
from typing import Any

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.operator import (
    AbstractOperator,
    ConcurrentSafe,
    DebugAware,
    MetricsAware,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
    StatsProvider,
)

try:
    from lupa import LuaRuntime  # type: ignore[import-untyped]
    HAS_LUPA = True
except ImportError:
    HAS_LUPA = False


class TransformByLua(
    AbstractOperator, ConcurrentSafe,
    StatsProvider, DebugAware, MetricsAware,
):
    def __init__(self):
        self._script = ""
        self._func_name = ""
        self._is_item_mode = True
        self._pool: _LuaPool | None = None
        self._operator_name = ""
        self._debug = False

    def init(self, params: OperatorParams):
        if not HAS_LUPA:
            raise OperatorException("transform_by_lua: lupa package is not installed")

        self._script = params.get_string("lua_script")
        func_for_item = params.get_string("function_for_item", "")
        func_for_common = params.get_string("function_for_common", "")

        if not func_for_item and not func_for_common:
            raise ValueError(
                "lua: exactly one of function_for_item or function_for_common must be set"
            )
        if func_for_item and func_for_common:
            raise ValueError(
                "lua: cannot set both function_for_item and function_for_common"
            )

        if func_for_item:
            self._func_name = func_for_item
            self._is_item_mode = True
        else:
            self._func_name = func_for_common
            self._is_item_mode = False

        # Validate script compiles and defines the function
        runtime = _create_lua_runtime()
        runtime.execute(self._script)
        fn = runtime.globals()[self._func_name]
        if fn is None:
            raise ValueError(
                f'lua: script does not define function "{self._func_name}"'
            )

        self._pool = _LuaPool(self._script)

    def set_debug(self, name: str, enabled: bool):
        self._operator_name = name
        self._debug = enabled

    def set_metrics_provider(self, provider: Any):
        self._metrics_provider = provider

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        if self._pool is None:
            raise OperatorException("lua: pool is not initialized")

        if self._debug:
            fields = len(self.common_input())
            non_nil = sum(1 for f in self.common_input() if input_.common(f) is not None)
            item_count = input_.item_count()
            mode = "item" if self._is_item_mode else "common"
            print(
                f'[pine:debug] operator="{self._operator_name}" '
                f"common_input fields={fields} non_nil={non_nil} "
                f"items={item_count} mode={mode} func={self._func_name}",
                file=sys.stderr,
            )

        runtime = self._pool.borrow()
        if runtime is None:
            raise OperatorException("lua: pool is closed")
        try:
            if self._is_item_mode:
                self._execute_for_item(token, runtime, input_, output)
            else:
                self._execute_for_common(token, runtime, input_, output)
        except OperatorException:
            raise
        except Exception as e:
            raise OperatorException(f"lua: {e}") from e
        finally:
            self._pool.return_state(runtime)

    def operator_stats(self) -> dict[str, int]:
        if self._pool is None:
            return {}
        return {
            "borrow_count": self._pool.borrow_count,
            "return_count": self._pool.return_count,
            "create_count": self._pool.create_count,
            "active_count": self._pool.active_count,
        }

    def _execute_for_item(
        self, token: CancellationToken, runtime: LuaRuntime,
        input_: OperatorInput, output: OperatorOutput,
    ):
        g = runtime.globals()

        # Set common fields as globals
        for field in self.common_input():
            g[field] = _to_lua(runtime, input_.common(field))

        fn = g[self._func_name]
        if fn is None:
            raise OperatorException(f'lua: function "{self._func_name}" not found')

        nret = len(self.item_output())
        n = input_.item_count()

        for i in range(n):
            if token.is_cancelled():
                break
            # Set item fields as globals
            for field in self.item_input():
                g[field] = _to_lua(runtime, input_.item(i, field))

            try:
                results = fn()
            except Exception as e:
                raise OperatorException(f"lua: item[{i}]: {e}") from e

            if nret == 1:
                val = _from_lua(results)
                if val is not None:
                    output.set_item(i, self.item_output()[0], val)
            elif nret > 1:
                # Multiple returns come as a tuple
                if isinstance(results, tuple):
                    for j in range(min(nret, len(results))):
                        val = _from_lua(results[j])
                        if val is not None:
                            output.set_item(i, self.item_output()[j], val)
                else:
                    val = _from_lua(results)
                    if val is not None:
                        output.set_item(i, self.item_output()[0], val)

    def _execute_for_common(
        self, token: CancellationToken, runtime: LuaRuntime,
        input_: OperatorInput, output: OperatorOutput,
    ):
        if token.is_cancelled():
            return

        g = runtime.globals()

        # Set common fields as globals
        for field in self.common_input():
            g[field] = _to_lua(runtime, input_.common(field))

        # Set item fields as Lua tables
        n = input_.item_count()
        for field in self.item_input():
            tbl = runtime.table()
            for i in range(n):
                tbl[i + 1] = _to_lua(runtime, input_.item(i, field))
            g[field] = tbl

        if token.is_cancelled():
            return

        fn = g[self._func_name]
        if fn is None:
            raise OperatorException(f'lua: function "{self._func_name}" not found')

        nret = len(self.common_output())
        results = fn()

        if nret == 1:
            output.set_common(self.common_output()[0], _from_lua(results))
        elif nret > 1:
            if isinstance(results, tuple):
                for j in range(min(nret, len(results))):
                    output.set_common(self.common_output()[j], _from_lua(results[j]))
            else:
                output.set_common(self.common_output()[0], _from_lua(results))


def _create_lua_runtime() -> "LuaRuntime":
    """Create a sandboxed Lua runtime."""
    runtime = LuaRuntime(unpack_returned_tuples=True)
    g = runtime.globals()
    # Remove dangerous functions
    g["dofile"] = None
    g["loadfile"] = None
    g["require"] = None
    g["package"] = None
    g["io"] = None
    g["os"] = None
    return runtime


def _to_lua(runtime: "LuaRuntime", v: Any) -> Any:
    """Convert Python value to Lua-compatible value."""
    if v is None:
        return None
    if isinstance(v, bool):
        return v
    if isinstance(v, (int, float)):
        return float(v)
    if isinstance(v, str):
        return v
    if isinstance(v, (list, tuple)):
        tbl = runtime.table()
        for i, elem in enumerate(v, 1):
            tbl[i] = _to_lua(runtime, elem)
        return tbl
    if isinstance(v, dict):
        tbl = runtime.table()
        for k, val in v.items():
            tbl[str(k)] = _to_lua(runtime, val)
        return tbl
    return str(v)


def _lua_type_name(v: Any) -> str:
    """Map a lupa-returned key to its Lua type name."""
    if isinstance(v, bool):
        return "boolean"
    if isinstance(v, (int, float)):
        return "number"
    if hasattr(v, "__len__") and hasattr(v, "__getitem__"):
        return "table"
    return "userdata"


def _from_lua(v: Any) -> Any:
    """Convert Lua value to Python value."""
    if v is None:
        return None
    if isinstance(v, bool):
        return v
    if isinstance(v, float):
        d = v
        if d == int(d) and not (d == float("inf") or d == float("-inf")):
            iv = int(d)
            if -(2**63) <= iv <= 2**63 - 1:
                return iv
        return d
    if isinstance(v, int):
        return v
    if isinstance(v, str):
        return v
    # lupa table objects — check for table-like interface
    if hasattr(v, "__len__") and hasattr(v, "__getitem__"):
        try:
            length = len(v)
        except Exception:
            length = 0
        if length > 0:
            arr = []
            for i in range(1, length + 1):
                arr.append(_from_lua(v[i]))
            return arr
        # String-keyed table
        m: dict[str, Any] = {}
        for k, val in v.items():
            if not isinstance(k, str):
                lua_type_name = _lua_type_name(k)
                raise OperatorException(
                    f'lua: table has non-string key of type "{lua_type_name}"'
                )
            m[k] = _from_lua(val)
        if not m:
            # Lua empty table → empty array (cross-runtime convention)
            return []
        return m
    return str(v)


class _LuaPool:
    """Pool of Lua runtime states for reuse."""

    def __init__(self, script: str):
        self._script = script
        self._pool: deque = deque()
        self._lock = threading.Lock()
        self._closed = False
        self.borrow_count = 0
        self.return_count = 0
        self.create_count = 0
        self.active_count = 0

        # Create initial state
        rt = _create_lua_runtime()
        rt.execute(script)
        self._pool.append(rt)
        self.create_count = 1

    def borrow(self) -> "LuaRuntime | None":
        with self._lock:
            if self._closed:
                return None
            self.borrow_count += 1
            self.active_count += 1
            if self._pool:
                return self._pool.popleft()
            self.create_count += 1
        rt = _create_lua_runtime()
        rt.execute(self._script)
        with self._lock:
            if self._closed:
                self.active_count -= 1
                del rt
                return None
        return rt

    def return_state(self, runtime: "LuaRuntime"):
        with self._lock:
            self.return_count += 1
            self.active_count -= 1
            if not self._closed:
                self._pool.append(runtime)

    def close(self):
        with self._lock:
            self._closed = True
            self._pool.clear()
