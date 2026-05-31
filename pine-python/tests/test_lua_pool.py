"""Tests for _LuaPool lifecycle, specifically close-during-borrow safety."""
from __future__ import annotations

import threading

import pytest

try:
    from lupa import LuaRuntime  # type: ignore[import-untyped]  # noqa: F401
    HAS_LUPA = True
except ImportError:
    HAS_LUPA = False

pytestmark = pytest.mark.skipif(not HAS_LUPA, reason="lupa not installed")


def _make_pool():
    from pine.operators.transform_by_lua import _LuaPool
    return _LuaPool("function noop() return 1 end")


def test_borrow_returns_none_after_close():
    pool = _make_pool()
    pool.close()
    assert pool.borrow() is None


def test_close_during_borrow_no_leak():
    """Simulate pool.close() racing with borrow() that needs to create a new state."""
    pool = _make_pool()

    # Drain the pool so next borrow must create a new state
    first = pool.borrow()
    assert first is not None

    barrier = threading.Barrier(2, timeout=5)
    close_done = threading.Event()

    def borrow_and_check():
        # This borrow will find pool empty, release lock, create state.
        # Meanwhile main thread closes the pool.
        result = pool.borrow()
        # Should get None because pool was closed during creation
        return result

    def close_after_barrier():
        barrier.wait()
        pool.close()
        close_done.set()

    # We need to inject the close between lock-release and lock-reacquire in borrow().
    # Monkey-patch the module-level _create_lua_runtime to synchronize.
    import pine.operators.transform_by_lua as lua_mod
    orig_create = lua_mod._create_lua_runtime

    def patched_create():
        rt = orig_create()
        # Signal main thread to close, then wait for it
        barrier.wait()
        close_done.wait(timeout=5)
        return rt

    lua_mod._create_lua_runtime = patched_create
    try:
        closer = threading.Thread(target=close_after_barrier)
        closer.start()

        result = pool.borrow()
        closer.join(timeout=5)

        # Pool was closed during creation, borrow should return None
        assert result is None
        assert pool.active_count == 1  # first borrow still counted (not returned)
    finally:
        lua_mod._create_lua_runtime = orig_create
        if first is not None:
            pool.return_state(first)


def test_normal_borrow_return_cycle():
    pool = _make_pool()
    rt = pool.borrow()
    assert rt is not None
    pool.return_state(rt)
    assert pool.borrow_count == 1
    assert pool.return_count == 1
    assert pool.active_count == 0
    pool.close()


def test_reuse_count_distinguishes_hits_from_misses():
    """reuse_count counts pool hits; misses = borrow_count - reuse_count."""
    pool = _make_pool()

    # Construction pre-warms exactly one state: a creation, not a borrow.
    assert pool.create_count == 1
    assert pool.reuse_count == 0

    def check_invariant():
        # Every borrow is a pool hit (reuse) or an on-borrow miss; create_count
        # also counts the single pre-warm creation, so misses = create_count - 1.
        assert pool.borrow_count == pool.reuse_count + (pool.create_count - 1)

    # Sequential reuse: return before borrowing again, so each borrow is a hit.
    for _ in range(5):
        rt = pool.borrow()
        assert rt is not None
        pool.return_state(rt)
        check_invariant()
    assert pool.reuse_count > 0

    # Force a miss: hold two states at once. The pool holds at most one idle
    # state, so the second borrow must create a fresh one.
    before_create = pool.create_count
    a = pool.borrow()
    b = pool.borrow()
    assert a is not None and b is not None
    assert pool.create_count > before_create
    check_invariant()
    pool.return_state(a)
    pool.return_state(b)
    check_invariant()
    pool.close()

