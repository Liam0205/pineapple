from __future__ import annotations

import json
import sys

from pine.cancellation import CancellationToken
from pine.operator import (
    AbstractOperator,
    ConsumesRowSet,
    OperatorInput,
    OperatorOutput,
    OperatorParams,
)


class ObserveLog(AbstractOperator, ConsumesRowSet):
    def __init__(self):
        self._prefix = ""

    def init(self, params: OperatorParams):
        v = params.get("log_prefix")
        if isinstance(v, str):
            self._prefix = v

    def execute(
        self, token: CancellationToken, input_: OperatorInput,
        output: OperatorOutput,
    ) -> None:
        snapshot: dict = {}

        if self.common_input():
            common: dict = {}
            for k in self.common_input():
                common[k] = input_.common(k)
            snapshot["common"] = common

        if self.item_input() and input_.item_count() > 0:
            items: list = []
            for i in range(input_.item_count()):
                row: dict = {}
                for k in self.item_input():
                    row[k] = input_.item(i, k)
                items.append(row)
            snapshot["items"] = items

        try:
            data = json.dumps(snapshot, ensure_ascii=False, default=str)
            if self._prefix:
                print(f"[observe_log] {self._prefix} {data}", file=sys.stderr)
            else:
                print(f"[observe_log] {data}", file=sys.stderr)
        except (TypeError, ValueError, OverflowError) as e:
            print(f"[observe_log] {self._prefix} marshal error: {e}", file=sys.stderr)
