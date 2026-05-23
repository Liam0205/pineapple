"""pine-python cause-chain-probe: smoke test for ExecutionError cause-chain
unwrap.

Constructs a FakeRedisError, wraps it via ``raise ... from`` into
pine.errors.ExecutionError, then uses ``isinstance(err.__cause__, T)`` to
recover the inner cause. Prints either::

    PASS:<recovered_inner_msg>

or::

    FAIL:<reason>

on stdout. cross-validate Section 15 asserts byte-identical stdout across
pine-go / pine-java / pine-python / pine-cpp probes.
"""
from __future__ import annotations

import sys

from pine.errors import ExecutionError


class FakeRedisError(Exception):
    def __init__(self, key: str):
        super().__init__(f"key={key} not found")
        self.key = key


def main() -> int:
    try:
        try:
            raise FakeRedisError("user:42")
        except FakeRedisError as inner:
            raise ExecutionError("redis_getter", inner) from inner
    except ExecutionError as outer:
        if isinstance(outer.__cause__, FakeRedisError):
            print(f"PASS:{outer.__cause__}")
            return 0
        print("FAIL:__cause__ did not recover FakeRedisError from ExecutionError chain")
        return 1


if __name__ == "__main__":
    sys.exit(main())
