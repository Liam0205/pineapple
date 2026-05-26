from __future__ import annotations

import threading
import math
from abc import ABC, abstractmethod
from typing import Any

from pine.config import InputFieldSpec
from pine.errors import ExecutionError
from pine.operator import OperatorInput, OperatorOutput


class Frame(ABC):
    """Abstract DataFrame holding common fields and item columns."""

    @staticmethod
    def create(
        storage_mode: str,
        common: dict[str, Any] | None,
        items: list[dict[str, Any]] | None,
    ) -> "Frame":
        # Route by storage_mode to match pine-go NewFrame /
        # pine-java Frame.create / pine-cpp pine::make_frame. Unknown /
        # empty values fall back to "column" (Go's NewFrame default).
        if storage_mode == "row":
            return RowFrame(common, items)
        return ColumnFrame(common, items)

    @abstractmethod
    def build_input(
        self,
        op_name: str,
        spec: InputFieldSpec,
    ) -> OperatorInput: ...

    @abstractmethod
    def apply_output(self, out: OperatorOutput, op_name: str, recall: bool) -> None: ...

    @abstractmethod
    def to_result_common(self, common_out: list[str]) -> dict[str, Any]: ...

    @abstractmethod
    def to_result_items(self, item_out: list[str]) -> list[dict[str, Any]]: ...

    @abstractmethod
    def item_count(self) -> int: ...

    @abstractmethod
    def check_skip(self, skip_fields: list[str]) -> bool: ...


class ColumnFrame(Frame):
    """Column-oriented Frame implementation with RLock concurrency."""

    def __init__(
        self, common: dict[str, Any] | None, items: list[dict[str, Any]] | None
    ):
        self._lock = threading.RLock()
        self._common: dict[str, Any] = dict(common) if common else {}
        self._row_count = len(items) if items else 0
        self._columns: dict[str, list[Any]] = {}
        self._presence: dict[str, list[bool]] = {}

        if items:
            all_fields: list[str] = []
            seen: set[str] = set()
            for row in items:
                for k in row:
                    if k not in seen:
                        all_fields.append(k)
                        seen.add(k)

            for field in all_fields:
                col = [None] * self._row_count
                bits = [False] * self._row_count
                for i, row in enumerate(items):
                    if field in row:
                        col[i] = row[field]
                        bits[i] = True
                self._columns[field] = col
                self._presence[field] = bits

    def item_count(self) -> int:
        with self._lock:
            return self._row_count

    def check_skip(self, skip_fields: list[str]) -> bool:
        with self._lock:
            for field in skip_fields:
                val = self._common.get(field)
                if val is not None and val is not False:
                    return True
            return False

    def build_input(
        self,
        op_name: str,
        spec: InputFieldSpec,
    ) -> OperatorInput:
        with self._lock:
            common_snapshot: dict[str, Any] = {}

            # Strict common fields
            for field in spec.strict_common:
                if field in self._common and self._common[field] is not None:
                    common_snapshot[field] = self._common[field]
                else:
                    raise ExecutionError(
                        op_name,
                        f'required field "{field}" is nil in common',
                    )
            # Defaulted common fields
            for df in spec.defaulted_common:
                if df.name in self._common and self._common[df.name] is not None:
                    common_snapshot[df.name] = self._common[df.name]
                else:
                    common_snapshot[df.name] = df.default

            items_snapshot: list[dict[str, Any]] = []
            for i in range(self._row_count):
                row: dict[str, Any] = {}

                # Strict item fields
                for field in spec.strict_item:
                    col = self._columns.get(field)
                    pres = self._presence.get(field)
                    if col is not None and pres is not None and pres[i] and col[i] is not None:
                        row[field] = col[i]
                    else:
                        raise ExecutionError(
                            op_name,
                            f'required field "{field}" is nil on item[{i}]',
                        )
                # Defaulted item fields
                for df in spec.defaulted_item:
                    col = self._columns.get(df.name)
                    pres = self._presence.get(df.name)
                    if col is not None and pres is not None and pres[i] and col[i] is not None:
                        row[df.name] = col[i]
                    else:
                        row[df.name] = df.default

                items_snapshot.append(row)

            return OperatorInput(common_snapshot, items_snapshot)

    def apply_output(self, out: OperatorOutput, op_name: str, recall: bool):
        with self._lock:
            # 1. Common writes
            for field, value in out.common_writes.items():
                v = _check_value(field, value)
                if v is not None:
                    raise ExecutionError(op_name, f"common write: {v}")
                self._common[field] = value

            # 2. Item writes
            for idx, writes in out.item_writes.items():
                if idx < 0 or idx >= self._row_count:
                    continue
                for field, value in writes.items():
                    v = _check_value(field, value)
                    if v is not None:
                        raise ExecutionError(op_name, f"item[{idx}] write: {v}")
                    if field not in self._columns:
                        self._columns[field] = [None] * self._row_count
                        self._presence[field] = [False] * self._row_count
                    self._columns[field][idx] = value
                    self._presence[field][idx] = True

            # 3. Remove items (single-pass in-place filter)
            if out.removed_items:
                removed = out.removed_items
                for field in self._columns:
                    col = self._columns[field]
                    pres = self._presence[field]
                    self._columns[field] = [
                        col[i] for i in range(self._row_count)
                        if i not in removed
                    ]
                    self._presence[field] = [
                        pres[i] for i in range(self._row_count)
                        if i not in removed
                    ]
                self._row_count -= len(removed)

            # 4. Reorder items
            if out.item_order is not None:
                order = out.item_order
                if len(order) != self._row_count:
                    raise ExecutionError(
                        op_name,
                        f"SetItemOrder length {len(order)} does not match item count {self._row_count}",
                    )
                # Length + OOB + permutation: without the permutation check,
                # set_item_order([0,0,0]) would silently duplicate item 0.
                seen = [False] * self._row_count
                for idx in order:
                    if idx < 0 or idx >= self._row_count:
                        raise ExecutionError(
                            op_name,
                            f"SetItemOrder index {idx} out of range [0, {self._row_count})",
                        )
                    if seen[idx]:
                        raise ExecutionError(
                            op_name,
                            f"SetItemOrder duplicate index {idx} (order must be a permutation)",
                        )
                    seen[idx] = True
                self._reindex(order)

            # 5. Add items (recall)
            if out.added_items:
                for item in out.added_items:
                    for field, value in item.items():
                        v = _check_value(field, value)
                        if v is not None:
                            raise ExecutionError(op_name, f"added item write: {v}")
                    new_row_idx = self._row_count
                    self._row_count += 1
                    for field in list(self._columns.keys()):
                        self._columns[field].append(None)
                        self._presence[field].append(False)
                    for field, value in item.items():
                        if field not in self._columns:
                            self._columns[field] = [None] * self._row_count
                            self._presence[field] = [False] * self._row_count
                        else:
                            # Column already extended above
                            pass
                        self._columns[field][new_row_idx] = value
                        self._presence[field][new_row_idx] = True
                    if recall:
                        if "_source" not in self._columns:
                            self._columns["_source"] = [None] * self._row_count
                            self._presence["_source"] = [False] * self._row_count
                        self._columns["_source"][new_row_idx] = op_name
                        self._presence["_source"][new_row_idx] = True

    def _reindex(self, indices: list[int]):
        for field in self._columns:
            col = self._columns[field]
            pres = self._presence[field]
            self._columns[field] = [col[i] for i in indices]
            self._presence[field] = [pres[i] for i in indices]
        self._row_count = len(indices)

    def to_result_common(self, common_out: list[str]) -> dict[str, Any]:
        with self._lock:
            if common_out is None:
                return dict(self._common)
            result: dict[str, Any] = {}
            for field in common_out:
                if field in self._common:
                    result[field] = self._common[field]
            return result

    def to_result_items(self, item_out: list[str]) -> list[dict[str, Any]]:
        with self._lock:
            items: list[dict[str, Any]] = []
            for i in range(self._row_count):
                row: dict[str, Any] = {}
                if item_out is None:
                    for field, col in self._columns.items():
                        pres = self._presence[field]
                        if pres[i]:
                            row[field] = col[i]
                else:
                    for field in item_out:
                        col = self._columns.get(field)
                        pres = self._presence.get(field)
                        if col is not None and pres is not None and pres[i]:
                            row[field] = col[i]
                items.append(row)
            return items


def _check_value(field: str, value):
    """Mirror pine-go validateValue (row_frame.go:224). Returns the
    violation message (without prefix) or None when OK. Reject NaN/Inf
    in any numeric write; everything else (str, bool, int, list, dict,
    None) passes through.
    """
    if value is None:
        return None
    if isinstance(value, bool):
        return None
    if isinstance(value, (int, float)):
        if isinstance(value, float) and (math.isnan(value) or math.isinf(value)):
            return f'field "{field}": NaN/Inf is not a valid JSON value'
        return None
    if isinstance(value, str):
        return None
    if isinstance(value, (list, dict)):
        return None
    return f'field "{field}": unsupported type {type(value).__name__}'


class RowFrame(Frame):
    """Row-oriented Frame implementation. Items are stored as a list of dicts,
    matching pine-go's RowFrame and pine-cpp's RowFrame.

    Trade-off vs ColumnFrame: cheaper for per-row access patterns (Lua
    snapshots, remote requests, observe logging, recall add_item) at the
    cost of column-wide batch scan speed.
    """

    def __init__(
        self, common: dict[str, Any] | None, items: list[dict[str, Any]] | None
    ):
        self._lock = threading.RLock()
        self._common: dict[str, Any] = dict(common) if common else {}
        self._items: list[dict[str, Any]] = [dict(r) for r in items] if items else []

    def item_count(self) -> int:
        with self._lock:
            return len(self._items)

    def check_skip(self, skip_fields: list[str]) -> bool:
        with self._lock:
            for field in skip_fields:
                val = self._common.get(field)
                if val is not None and val is not False:
                    return True
            return False

    def build_input(
        self,
        op_name: str,
        spec: InputFieldSpec,
    ) -> OperatorInput:
        with self._lock:
            common_snapshot: dict[str, Any] = {}

            for field in spec.strict_common:
                if field in self._common and self._common[field] is not None:
                    common_snapshot[field] = self._common[field]
                else:
                    raise ExecutionError(
                        op_name,
                        f'required field "{field}" is nil in common',
                    )
            for df in spec.defaulted_common:
                if df.name in self._common and self._common[df.name] is not None:
                    common_snapshot[df.name] = self._common[df.name]
                else:
                    common_snapshot[df.name] = df.default

            items_snapshot: list[dict[str, Any]] = []
            for i, row in enumerate(self._items):
                snap: dict[str, Any] = {}
                for field in spec.strict_item:
                    if field in row and row[field] is not None:
                        snap[field] = row[field]
                    else:
                        raise ExecutionError(
                            op_name,
                            f'required field "{field}" is nil on item[{i}]',
                        )
                for df in spec.defaulted_item:
                    if df.name in row and row[df.name] is not None:
                        snap[df.name] = row[df.name]
                    else:
                        snap[df.name] = df.default
                items_snapshot.append(snap)

            return OperatorInput(common_snapshot, items_snapshot)

    def apply_output(self, out: OperatorOutput, op_name: str, recall: bool):
        with self._lock:
            # 1. Common writes
            for field, value in out.common_writes.items():
                v = _check_value(field, value)
                if v is not None:
                    raise ExecutionError(op_name, f"common write: {v}")
                self._common[field] = value

            # 2. Item writes
            for idx, writes in out.item_writes.items():
                if idx < 0 or idx >= len(self._items):
                    continue
                for field, value in writes.items():
                    v = _check_value(field, value)
                    if v is not None:
                        raise ExecutionError(op_name, f"item[{idx}] write: {v}")
                    self._items[idx][field] = value

            # 3. Remove items
            if out.removed_items:
                removed = out.removed_items
                self._items = [
                    self._items[i] for i in range(len(self._items)) if i not in removed
                ]

            # 4. Reorder items
            if out.item_order is not None:
                order = out.item_order
                if len(order) != len(self._items):
                    raise ExecutionError(
                        op_name,
                        f"SetItemOrder length {len(order)} does not match item count {len(self._items)}",
                    )
                seen = [False] * len(self._items)
                for idx in order:
                    if idx < 0 or idx >= len(self._items):
                        raise ExecutionError(
                            op_name,
                            f"SetItemOrder index {idx} out of range [0, {len(self._items)})",
                        )
                    if seen[idx]:
                        raise ExecutionError(
                            op_name,
                            f"SetItemOrder duplicate index {idx} (order must be a permutation)",
                        )
                    seen[idx] = True
                self._items = [self._items[i] for i in order]

            # 5. Add items (recall stamps _source)
            if out.added_items:
                for item in out.added_items:
                    for field, value in item.items():
                        v = _check_value(field, value)
                        if v is not None:
                            raise ExecutionError(op_name, f"added item write: {v}")
                    new_row = dict(item)
                    if recall:
                        new_row["_source"] = op_name
                    self._items.append(new_row)

    def to_result_common(self, common_out: list[str]) -> dict[str, Any]:
        with self._lock:
            if common_out is None:
                return dict(self._common)
            return {
                field: self._common[field]
                for field in common_out
                if field in self._common
            }

    def to_result_items(self, item_out: list[str]) -> list[dict[str, Any]]:
        with self._lock:
            items: list[dict[str, Any]] = []
            for row in self._items:
                if item_out is None:
                    # Keep explicit nulls — pine-go RowFrame.ToResult /
                    # ColumnFrame.ToResult both preserve PRESENT-NULL,
                    # only ABSENT keys are stripped. (Dual-impl
                    # equivalence requires the same projection rule.)
                    items.append(dict(row))
                else:
                    items.append(
                        {field: row[field] for field in item_out if field in row}
                    )
            return items
