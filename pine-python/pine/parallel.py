"""Parallel (sharded) operator execution logic."""
from __future__ import annotations

import threading
from typing import Any

from pine.cancellation import CancellationToken
from pine.operator import Operator, OperatorInput, OperatorOutput


def _parallel_execute(
    token: CancellationToken,
    operator: Operator,
    input_: OperatorInput,
    data_parallel: int,
    op_name: str,
) -> OperatorOutput:
    """Execute an operator in parallel shards over items.

    Splits items into N shards, executes each with a child CancellationToken,
    and merges outputs. Only called for ConcurrentSafe operators.
    """
    items = input_.raw_items()
    item_count = len(items)
    shard_count = min(data_parallel, max(item_count, 1))

    if shard_count <= 1:
        output = OperatorOutput()
        operator.execute(token, input_, output)
        return output

    # Split items into shards
    shards: list[list[dict[str, Any]]] = [[] for _ in range(shard_count)]
    shard_indices: list[list[int]] = [[] for _ in range(shard_count)]
    for i, item in enumerate(items):
        shard_idx = i % shard_count
        shards[shard_idx].append(item)
        shard_indices[shard_idx].append(i)

    # Execute each shard with a child token
    shard_outputs: list[OperatorOutput | None] = [None] * shard_count
    shard_errors: list[Exception | None] = [None] * shard_count
    child_token = token.child()

    def _execute_shard(shard_i: int):
        if token.is_cancelled() or child_token.is_cancelled():
            return
        shard_input = OperatorInput(input_.raw_common(), shards[shard_i])
        shard_out = OperatorOutput()
        try:
            operator.execute(child_token, shard_input, shard_out)
            shard_outputs[shard_i] = shard_out
        except Exception as e:
            shard_errors[shard_i] = e
            child_token.cancel()

    threads = []
    for si in range(shard_count):
        t = threading.Thread(target=_execute_shard, args=(si,), daemon=True)
        threads.append(t)
        t.start()
    for t in threads:
        t.join()

    # Check for shard errors
    for err in shard_errors:
        if err is not None:
            raise err

    # Merge shard outputs back into a single OperatorOutput
    merged = OperatorOutput()
    for si in range(shard_count):
        shard_out = shard_outputs[si]
        if shard_out is None:
            continue

        # Merge common writes (last writer wins)
        for key, value in shard_out.common_writes.items():
            merged.set_common(key, value)

        # Merge item writes (remap shard-local indices to global)
        for local_idx, writes in shard_out.item_writes.items():
            if local_idx < len(shard_indices[si]):
                global_idx = shard_indices[si][local_idx]
                for field_name, value in writes.items():
                    merged.set_item(global_idx, field_name, value)

    return merged
