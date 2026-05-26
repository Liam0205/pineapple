from __future__ import annotations

import ipaddress
import json
import socket
from typing import Any

from pine.cancellation import CancellationToken
from pine.errors import OperatorException
from pine.operator import (
    AbstractOperator,
    ConcurrentSafe,
    ConsumesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)

try:
    import httpx  # type: ignore[import-untyped]
    HAS_HTTPX = True
except ImportError:
    HAS_HTTPX = False


class TransformRemotePineapple(AbstractOperator, ConcurrentSafe, ConsumesRowSet):
    def __init__(self):
        self._url = ""
        self._host = ""
        self._timeout = 5.0
        self._fail_on_error = True
        self._allow_private = False
        self._max_response_size = 10 * 1024 * 1024
        self._client: Any = None
        self._common_req: list[str] = []
        self._item_req: list[str] = []
        self._common_resp: list[str] = []
        self._item_resp: list[str] = []

    def init(self, params: OperatorParams):
        if not HAS_HTTPX:
            raise OperatorException(
                "transform_by_remote_pineapple: httpx package is not installed"
            )

        self._host = params.get_string("host", "")
        port = params.get_int("port", 0)
        endpoint = params.get_string("endpoint", "/execute")
        if not endpoint:
            endpoint = "/execute"

        self._url = f"http://{self._host}:{port}{endpoint}"

        timeout_val = params.get("timeout")
        if isinstance(timeout_val, (int, float)):
            self._timeout = float(timeout_val)

        foe = params.get("fail_on_error")
        if isinstance(foe, bool):
            self._fail_on_error = foe

        mrs = params.get("max_response_size")
        if isinstance(mrs, (int, float)):
            self._max_response_size = int(mrs)

        ap = params.get("allow_private")
        if isinstance(ap, bool):
            self._allow_private = ap

        self._common_req = _to_string_list(params.get("common_request"))
        self._item_req = _to_string_list(params.get("item_request"))
        self._common_resp = _to_string_list(params.get("common_response"))
        self._item_resp = _to_string_list(params.get("item_response"))

        if not self._allow_private:
            _validate_host(self._host)

        # httpx.Client needs explicit close(), but Operator has no lifecycle hook.
        # Go/Java HTTP clients don't need close (GC'd / no close pre-Java 21).
        # This client is process-scoped (Engine lifetime); OS reclaims on exit.
        self._client = httpx.Client(timeout=self._timeout)

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        c_req = self._common_req if self._common_req else self.common_input()
        i_req = self._item_req if self._item_req else self.item_input()
        c_resp = self._common_resp if self._common_resp else self.common_output()
        i_resp = self._item_resp if self._item_resp else self.item_output()

        # Build request common
        req_common: dict[str, Any] = {}
        for i in range(min(len(self.common_input()), len(c_req))):
            req_common[c_req[i]] = input_.common(self.common_input()[i])

        # Build request items
        req_items: list[dict[str, Any]] = []
        for j in range(input_.item_count()):
            item: dict[str, Any] = {}
            for i in range(min(len(self.item_input()), len(i_req))):
                item[i_req[i]] = input_.item(j, self.item_input()[i])
            req_items.append(item)

        req_body = {"common": req_common, "items": req_items}

        try:
            body = json.dumps(req_body, ensure_ascii=False, default=str).encode("utf-8")
        except Exception as e:
            raise OperatorException(
                f"transform_by_remote_pineapple: serialize request: {e}"
            ) from e

        if token.is_cancelled():
            return

        try:
            if not self._allow_private:
                _validate_host_at_dial_time(self._host)
            resp = self._client.post(
                self._url,
                content=body,
                headers={"Content-Type": "application/json"},
            )
        except Exception as e:
            self._handle_error(output, f"request failed: {e}", e)
            return

        # Check response size
        resp_body = resp.content
        if len(resp_body) > self._max_response_size:
            self._handle_error(
                output,
                f"response body exceeds {self._max_response_size} bytes limit",
                None,
            )
            return

        if resp.status_code != 200:
            self._handle_error(
                output,
                f"HTTP {resp.status_code}: {_truncate_body(resp_body)}",
                None,
            )
            return

        try:
            result = json.loads(resp_body)
        except Exception as e:
            self._handle_error(output, f"parse response: {e}", e)
            return

        err_obj = result.get("error")
        if isinstance(err_obj, str) and err_obj:
            self._handle_error(output, f"downstream error: {err_obj}", None)
            return

        resp_common = result.get("common", {})
        resp_items = result.get("items", [])

        # Map common response fields
        for i in range(min(len(self.common_output()), len(c_resp))):
            remote_field = c_resp[i]
            if remote_field in resp_common:
                output.set_common(self.common_output()[i], resp_common[remote_field])

        # Map item response fields
        for j in range(min(input_.item_count(), len(resp_items))):
            resp_item = resp_items[j]
            for i in range(min(len(self.item_output()), len(i_resp))):
                remote_field = i_resp[i]
                if remote_field in resp_item:
                    output.set_item(j, self.item_output()[i], resp_item[remote_field])

    def _handle_error(self, output: OperatorOutput, msg: str, cause: Exception | None):
        full_msg = f"transform_by_remote_pineapple: {msg}"
        if self._fail_on_error:
            raise OperatorException(full_msg) from cause
        output.set_warning(OperatorException(full_msg))


_ERROR_BODY_MAX = 1024


def _truncate_body(body: bytes) -> str:
    """Clip a downstream response body to _ERROR_BODY_MAX bytes for error
    messages / warnings. P1-E4 — keeps a 5 MB HTML 500 page from
    fanning out into log/JSON/exception streams as-is."""
    if len(body) <= _ERROR_BODY_MAX:
        return body.decode("utf-8", errors="replace")
    head = body[:_ERROR_BODY_MAX].decode("utf-8", errors="replace")
    return f"{head}...(truncated, total {len(body)} bytes)"


def _validate_host(host: str):
    """Validate host is not a private/loopback address."""
    if not host or host == "localhost":
        raise ValueError(
            f'transform_by_remote_pineapple: host "{host}" is not allowed '
            f"(private/loopback)"
        )
    try:
        addrs = socket.getaddrinfo(host, None)
        for _, _, _, _, sockaddr in addrs:
            addr = ipaddress.ip_address(sockaddr[0])
            if _is_private_address(addr):
                raise ValueError(
                    f'transform_by_remote_pineapple: host "{host}" resolves to '
                    f"private address {addr}"
                )
    except socket.gaierror:
        # DNS may not be available at init; dial-time check is the real guard
        pass


def _validate_host_at_dial_time(host: str):
    """SSRF check at dial time."""
    addrs = socket.getaddrinfo(host, None)
    for _, _, _, _, sockaddr in addrs:
        addr = ipaddress.ip_address(sockaddr[0])
        if _is_private_address(addr):
            raise OperatorException(
                f'transform_by_remote_pineapple: dial-time SSRF check failed: '
                f'"{host}" resolves to private address {addr}'
            )


def _is_private_address(addr: ipaddress.IPv4Address | ipaddress.IPv6Address) -> bool:
    """Check if address is loopback, private, or link-local."""
    return addr.is_loopback or addr.is_private or addr.is_link_local


def _to_string_list(v: Any) -> list[str]:
    """Convert value to list of strings."""
    if isinstance(v, list):
        return [str(elem) for elem in v]
    return []
