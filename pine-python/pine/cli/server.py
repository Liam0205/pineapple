"""Pine HTTP server -- compatible with Go/Java pineapple-server."""
from __future__ import annotations

import json
import sys
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path
from typing import Any, Callable

from pine.engine import Engine, StaticResourceProvider
from pine.errors import ConfigError, RegistryError, ValidationError
from pine.go_format import go_json_marshal

_DEFAULT_MAX_BODY = 10 * 1024 * 1024  # 10MB

Middleware = Callable[["_PineHandler", Callable[[], None]], None]


class _ServerState:
    """Thread-safe mutable server state (engine + reload stats)."""

    def __init__(self, engine: Engine):
        self._lock = threading.Lock()
        self._engine = engine
        self.reload_count = 0
        self.reload_error_count = 0
        self.last_reload_duration_ns = 0

    @property
    def engine(self) -> Engine:
        with self._lock:
            return self._engine

    def swap_engine(self, new_engine: Engine, duration_ns: int):
        with self._lock:
            self._engine = new_engine
            self.reload_count += 1
            self.last_reload_duration_ns = duration_ns

    def record_reload_error(self):
        with self._lock:
            self.reload_error_count += 1

    def server_stats(self) -> dict[str, Any]:
        with self._lock:
            return {
                "last_reload_duration_ns": self.last_reload_duration_ns,
                "reload_count": self.reload_count,
                "reload_error_count": self.reload_error_count,
            }


class _PineHandler(BaseHTTPRequestHandler):
    state: _ServerState
    max_body: int
    middlewares: list[Middleware]

    def log_message(self, format, *args):
        pass

    def _dispatch(self):
        """Route request to internal handlers or 404."""
        path = self.path.split("?")[0]
        method = self.command

        if method == "GET":
            if path == "/health":
                self._json_response(200, {"status": "ok"})
            elif path == "/stats":
                self._handle_stats()
            elif path == "/dag":
                self._handle_dag()
            elif path == "/execute":
                self._method_not_allowed()
            else:
                self._json_response(404, {"error": "not found"})
        elif method == "POST":
            if path == "/execute":
                self._handle_execute()
            elif path in ("/health", "/stats", "/dag"):
                self._method_not_allowed()
            else:
                self._json_response(404, {"error": "not found"})
        else:
            self._method_not_allowed()

    def _run_middleware_chain(self):
        """Execute middleware chain, then dispatch."""
        chain = self.middlewares

        def build_next(idx: int) -> Callable[[], None]:
            if idx >= len(chain):
                return self._dispatch
            return lambda: chain[idx](self, build_next(idx + 1))

        build_next(0)()

    def do_GET(self):
        self._run_middleware_chain()

    def do_POST(self):
        self._run_middleware_chain()

    def _method_not_allowed(self):
        self._json_response(405, {"error": "method not allowed"})

    def _handle_execute(self):
        content_length = int(self.headers.get("Content-Length", 0))
        if content_length > self.max_body:
            self._json_response(413, {"error": "request body too large"})
            return

        body = self.rfile.read(content_length)
        if len(body) > self.max_body:
            self._json_response(413, {"error": "request body too large"})
            return

        try:
            req = json.loads(body)
        except (json.JSONDecodeError, ValueError) as e:
            self._json_response(400, {"error": f"invalid request: {e}"})
            return

        if not isinstance(req, dict):
            self._json_response(400, {"error": "invalid request: expected JSON object"})
            return

        common = req.get("common")
        items = req.get("items")

        return_trace = False
        if common and isinstance(common, dict):
            return_trace = common.pop("_return_trace", False) is True

        try:
            result = self.state.engine.execute(common, items)
        except ValidationError as e:
            error_msg = f"pine: validation error: {e}"
            resp: dict[str, Any] = {"common": None, "items": None, "error": error_msg}
            self._json_response(400, resp)
            return

        resp = _build_response(result, return_trace)

        if result.error is not None:
            self._json_response(500, resp)
        else:
            self._json_response(200, resp)

    def _handle_stats(self):
        engine = self.state.engine
        stats = engine.stats()
        sched = engine.scheduler_stats()
        custom = engine.operator_custom_stats()

        resp: dict[str, Any] = {
            "operators": stats,
            "scheduler": sched,
            "server": self.state.server_stats(),
        }

        if custom:
            resp["operator_detail"] = custom

        self._json_response(200, resp)

    def _handle_dag(self):
        params = _parse_query(self.path)
        fmt = params.get("format", "dot")
        collapse_str = params.get("collapse", "0")

        try:
            collapse = int(collapse_str)
            if collapse < 0:
                raise ValueError()
        except ValueError:
            self._json_response(400, {"error": "collapse must be a non-negative integer"})
            return

        try:
            output = self.state.engine.render_dag(fmt, collapse)
        except (ValidationError, ValueError) as e:
            self._json_response(400, {"error": str(e)})
            return

        if fmt == "mermaid":
            ct = "text/plain; charset=utf-8"
        else:
            ct = "text/vnd.graphviz; charset=utf-8"

        self.send_response(200)
        self.send_header("Content-Type", ct)
        encoded = output.encode("utf-8")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

    def _json_response(self, status: int, obj: Any):
        body = go_json_marshal(obj) + "\n"
        encoded = body.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)


def _build_response(result: Any, return_trace: bool) -> dict[str, Any]:
    resp: dict[str, Any] = {
        "common": result.common,
        "items": result.items,
    }

    if result.warnings:
        resp["warnings"] = [
            f'operator "{w.operator}": {w.err}' for w in result.warnings
        ]

    if return_trace and result.trace:
        trace_list = []
        for t in result.trace:
            if t.duration_ns == 0 and not t.skipped:
                continue
            entry: dict[str, Any] = {
                "name": t.name,
                "duration_ms": t.duration_ns / 1_000_000.0,
            }
            if t.skipped:
                entry["skipped"] = True
            if t.input_snapshot is not None:
                entry["input_snapshot"] = t.input_snapshot
            if t.output_snapshot is not None:
                entry["output_snapshot"] = t.output_snapshot
            trace_list.append(entry)
        if trace_list:
            resp["trace"] = trace_list

    if result.error is not None:
        resp["error"] = str(result.error)

    return resp


def _parse_query(path: str) -> dict[str, str]:
    params: dict[str, str] = {}
    if "?" in path:
        query = path.split("?", 1)[1]
        for part in query.split("&"):
            if "=" in part:
                k, v = part.split("=", 1)
                params[k] = v
            else:
                params[part] = ""
    return params


def _watch_config(state: _ServerState, config_path: str, resource_provider: Any,
                   stop_event: threading.Event):
    """Poll config file for changes and hot-reload the engine."""
    path = Path(config_path)
    try:
        last_mod = path.stat().st_mtime
    except OSError:
        last_mod = 0.0

    while not stop_event.is_set():
        if stop_event.wait(timeout=2):
            break
        try:
            cur_mod = path.stat().st_mtime
        except OSError:
            continue
        if cur_mod <= last_mod:
            continue
        last_mod = cur_mod
        start_ns = time.perf_counter_ns()
        try:
            data = path.read_bytes()
            new_engine = Engine.create(data, resource_provider=resource_provider)
            duration_ns = time.perf_counter_ns() - start_ns
            state.swap_engine(new_engine, duration_ns)
            print(f"config reloaded from {config_path}", file=sys.stderr)
        except Exception as e:
            state.record_reload_error()
            print(f"config reload failed: {e}", file=sys.stderr)


class PineServer:
    """Programmatic Pine server with middleware support.

    Usage:
        server = PineServer(config_path, port=9000)
        server.add_middleware(my_middleware)
        server.start()  # blocks
    """

    def __init__(self, config_path: str, *, port: int = 8080, host: str = "",
                 max_body: int = _DEFAULT_MAX_BODY,
                 resource_provider: Any = None):
        from pine.operators import ensure_registered
        ensure_registered()

        self._config_path = config_path
        self._host = host
        self._port = port
        self._max_body = max_body
        self._resource_provider = resource_provider
        self._middlewares: list[Middleware] = []
        self._started = False
        self._server: HTTPServer | None = None
        self._stop_event = threading.Event()

    def add_middleware(self, mw: Middleware):
        if self._started:
            raise RuntimeError("cannot add middleware after server has started")
        self._middlewares.append(mw)

    def start(self):
        """Start the server (blocking)."""
        self._started = True
        config_data = Path(self._config_path).read_bytes()

        try:
            engine = Engine.create(config_data, resource_provider=self._resource_provider)
        except (ConfigError, RegistryError) as e:
            raise RuntimeError(f"error creating engine: {e}") from e

        state = _ServerState(engine)

        handler = type("Handler", (_PineHandler,), {
            "state": state,
            "max_body": self._max_body,
            "middlewares": list(self._middlewares),
        })

        watcher = threading.Thread(
            target=_watch_config,
            args=(state, self._config_path, self._resource_provider, self._stop_event),
            daemon=True,
        )
        watcher.start()

        self._server = HTTPServer((self._host, self._port), handler)
        print(f"Pine server listening on :{self._port}", file=sys.stderr)

        try:
            self._server.serve_forever()
        except KeyboardInterrupt:
            self.stop()

    def stop(self):
        self._stop_event.set()
        if self._server:
            self._server.shutdown()


def main():
    from pine.operators import ensure_registered
    ensure_registered()

    config_path = ""
    addr = ":8080"
    max_body = _DEFAULT_MAX_BODY
    resources_path = ""

    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "-config" and i + 1 < len(args):
            i += 1
            config_path = args[i]
        elif args[i] == "-addr" and i + 1 < len(args):
            i += 1
            addr = args[i]
        elif args[i] == "-max-body-size" and i + 1 < len(args):
            i += 1
            max_body = int(args[i])
        elif args[i] == "-static-resources" and i + 1 < len(args):
            i += 1
            resources_path = args[i]
        i += 1

    if not config_path:
        print("Usage: server -config <path> [-addr :8080] [-max-body-size 10485760]",
              file=sys.stderr)
        sys.exit(1)

    config_data = Path(config_path).read_bytes()

    resource_provider = None
    if resources_path:
        res_data = json.loads(Path(resources_path).read_bytes())
        resource_provider = StaticResourceProvider(res_data)

    try:
        engine = Engine.create(config_data, resource_provider=resource_provider)
    except (ConfigError, RegistryError) as e:
        print(f"error creating engine: {e}", file=sys.stderr)
        sys.exit(1)

    state = _ServerState(engine)

    host = ""
    port = 8080
    if addr.startswith(":"):
        port = int(addr[1:])
    else:
        parts = addr.rsplit(":", 1)
        host = parts[0]
        port = int(parts[1]) if len(parts) > 1 else 8080

    handler = type("Handler", (_PineHandler,), {
        "state": state,
        "max_body": max_body,
        "middlewares": [],
    })

    stop_event = threading.Event()

    watcher = threading.Thread(
        target=_watch_config,
        args=(state, config_path, resource_provider, stop_event),
        daemon=True,
    )
    watcher.start()

    server = HTTPServer((host, port), handler)
    print(f"Pine server listening on {addr}", file=sys.stderr)

    try:
        server.serve_forever()
    except KeyboardInterrupt:
        stop_event.set()
        server.shutdown()


if __name__ == "__main__":
    main()
