"""Server extensibility tests for pine-python.

Verifies:
- Unknown paths return JSON 404 with {"error": "not found"}
- Middleware can intercept custom paths (e.g. /metrics)
- Method not allowed returns JSON 405
"""
from __future__ import annotations

import json
import threading
import time
from http.client import HTTPConnection
from pathlib import Path
from typing import Any, Callable

import pytest

from pine.cli.server import PineServer, _PineHandler, _ServerState, Middleware
from pine.engine import Engine

FIXTURES_ROOT = Path(__file__).parent.parent.parent / "fixtures"


def _make_config() -> str:
    """Create a minimal temp config file and return its path."""
    cfg = {
        "pipeline_config": {
            "operators": {
                "noop": {
                    "type_name": "transform_copy",
                    "direction": "common_to_common",
                    "$metadata": {
                        "common_input": ["x"],
                        "common_output": ["y"],
                        "item_input": [],
                        "item_output": [],
                    },
                }
            }
        },
        "pipeline_group": {"main": {"pipeline": ["noop"]}},
        "flow_contract": {
            "common_input": ["x"],
            "item_input": [],
            "common_output": ["x", "y"],
            "item_output": [],
        },
    }
    import tempfile

    f = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
    json.dump(cfg, f)
    f.close()
    return f.name


def _find_free_port() -> int:
    import socket

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("", 0))
        return s.getsockname()[1]


def _wait_ready(port: int, timeout: float = 5.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        try:
            conn = HTTPConnection("localhost", port, timeout=1)
            conn.request("GET", "/health")
            resp = conn.getresponse()
            if resp.status == 200:
                conn.close()
                return
            conn.close()
        except (ConnectionRefusedError, OSError):
            pass
        time.sleep(0.1)
    raise RuntimeError(f"Server on port {port} not ready within {timeout}s")


class TestServerNotFound:
    """Unknown paths must return JSON 404."""

    @pytest.fixture(autouse=True)
    def server(self):
        config_path = _make_config()
        port = _find_free_port()
        srv = PineServer(config_path, port=port)
        t = threading.Thread(target=srv.start, daemon=True)
        t.start()
        _wait_ready(port)
        self.port = port
        self.srv = srv
        yield
        srv.stop()
        Path(config_path).unlink(missing_ok=True)

    def test_unknown_path_returns_json_404(self):
        conn = HTTPConnection("localhost", self.port)
        conn.request("GET", "/unknown-path")
        resp = conn.getresponse()
        assert resp.status == 404
        body = json.loads(resp.read())
        assert body == {"error": "not found"}
        assert "application/json" in resp.getheader("Content-Type", "")
        conn.close()

    def test_post_unknown_path_returns_json_404(self):
        conn = HTTPConnection("localhost", self.port)
        conn.request("POST", "/does-not-exist")
        resp = conn.getresponse()
        assert resp.status == 404
        body = json.loads(resp.read())
        assert body == {"error": "not found"}
        conn.close()

    def test_method_not_allowed_json(self):
        conn = HTTPConnection("localhost", self.port)
        conn.request("POST", "/health")
        resp = conn.getresponse()
        assert resp.status == 405
        body = json.loads(resp.read())
        assert body == {"error": "method not allowed"}
        conn.close()


class TestServerMiddleware:
    """Middleware can intercept custom paths."""

    @pytest.fixture(autouse=True)
    def server(self):
        config_path = _make_config()
        port = _find_free_port()
        srv = PineServer(config_path, port=port)

        def metrics_middleware(handler: _PineHandler, next_fn: Callable[[], None]):
            path = handler.path.split("?")[0]
            if path == "/metrics":
                handler._json_response(200, {"custom": True})
                return
            next_fn()

        srv.add_middleware(metrics_middleware)
        t = threading.Thread(target=srv.start, daemon=True)
        t.start()
        _wait_ready(port)
        self.port = port
        self.srv = srv
        yield
        srv.stop()
        Path(config_path).unlink(missing_ok=True)

    def test_middleware_intercepts_custom_path(self):
        conn = HTTPConnection("localhost", self.port)
        conn.request("GET", "/metrics")
        resp = conn.getresponse()
        assert resp.status == 200
        body = json.loads(resp.read())
        assert body["custom"] is True
        conn.close()

    def test_known_path_still_works(self):
        conn = HTTPConnection("localhost", self.port)
        conn.request("GET", "/health")
        resp = conn.getresponse()
        assert resp.status == 200
        body = json.loads(resp.read())
        assert body["status"] == "ok"
        conn.close()

    def test_unknown_path_not_intercepted(self):
        conn = HTTPConnection("localhost", self.port)
        conn.request("GET", "/other")
        resp = conn.getresponse()
        assert resp.status == 404
        body = json.loads(resp.read())
        assert body == {"error": "not found"}
        conn.close()


class TestServerMiddlewareCannotAddAfterStart:
    """Adding middleware after start raises RuntimeError."""

    def test_add_middleware_after_start_raises(self):
        config_path = _make_config()
        port = _find_free_port()
        srv = PineServer(config_path, port=port)
        t = threading.Thread(target=srv.start, daemon=True)
        t.start()
        _wait_ready(port)
        try:
            with pytest.raises(RuntimeError, match="cannot add middleware"):
                srv.add_middleware(lambda h, n: n())
        finally:
            srv.stop()
            Path(config_path).unlink(missing_ok=True)
