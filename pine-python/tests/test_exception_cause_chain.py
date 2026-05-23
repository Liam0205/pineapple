"""Tests for ExecutionError / PanicError cause chain (issue #34).

pine-go ExecutionError.Unwrap() and pine-cpp std::nested_exception both
let downstream code walk the cause chain to identify the original
exception type. pine-python achieves the same via Python's standard
``__cause__`` attribute + ``raise ... from ...`` mechanism.
"""
from pine.errors import ExecutionError, PanicError


class FakeRedisError(Exception):
    def __init__(self, key: str):
        super().__init__(f"key={key} not found")
        self.key = key


class FakeTimeoutError(Exception):
    pass


def test_execution_error_with_string_cause_keeps_message_byte_exact():
    """Legacy 2-arg (str) form must keep args[0] format identical to
    pre-#34 behaviour so cross-validate Section 5 fixtures don't drift."""
    err = ExecutionError("op_x", "required field nil")
    assert str(err) == 'operator "op_x": required field nil'
    assert err.operator == "op_x"
    assert err.__cause__ is None


def test_execution_error_with_exception_cause_sets_dunder_cause():
    inner = FakeRedisError("foo")
    err = ExecutionError("redis_getter", inner)
    # __cause__ exposes the original exception object for isinstance checks
    assert err.__cause__ is inner
    assert isinstance(err.__cause__, FakeRedisError)
    # str form still includes the inner message for byte-exact log/JSON
    assert str(err) == 'operator "redis_getter": key=foo not found'
    assert err.operator == "redis_getter"


def test_execution_error_cause_chain_walkable():
    """Downstream pattern: catch outer ExecutionError, identify inner type."""
    inner = FakeRedisError("bar")
    try:
        raise ExecutionError("redis_getter", inner) from inner
    except ExecutionError as e:
        assert isinstance(e.__cause__, FakeRedisError)
        assert e.__cause__.key == "bar"


def test_execution_error_string_form_does_not_set_cause():
    """Construction with str must not invent a __cause__."""
    err = ExecutionError("op_x", "no inner here")
    assert err.__cause__ is None


def test_panic_error_with_exception_cause_sets_dunder_cause():
    inner = FakeTimeoutError("deadline exceeded")
    panic = PanicError("pine: panic in operator \"op_x\": ...", detail="", cause=inner)
    assert panic.__cause__ is inner
    assert isinstance(panic.__cause__, FakeTimeoutError)


def test_panic_error_without_cause_keyword_backwards_compatible():
    """Legacy callers that pass only (message, detail) still work."""
    panic = PanicError("pine: panic in operator \"op_x\": boom", detail="stack...")
    assert str(panic) == 'pine: panic in operator "op_x": boom'
    assert panic.detail == "stack..."
    assert panic.__cause__ is None


def test_raise_from_chain_preserves_full_path():
    """Multi-level chain: deepest cause should still be reachable."""
    try:
        try:
            raise FakeRedisError("deep")
        except FakeRedisError as inner:
            raise ValueError("middle") from inner
    except ValueError as middle:
        try:
            raise ExecutionError("op", middle) from middle
        except ExecutionError as outer:
            assert isinstance(outer.__cause__, ValueError)
            assert isinstance(outer.__cause__.__cause__, FakeRedisError)
            assert outer.__cause__.__cause__.key == "deep"


def test_execution_error_legacy_single_arg_emits_deprecation_warning():
    """Legacy `raise ExecutionError("msg")` must still work but warn."""
    import warnings as warnings_mod
    with warnings_mod.catch_warnings(record=True) as caught:
        warnings_mod.simplefilter("always")
        err = ExecutionError("legacy message")
        assert any(issubclass(w.category, DeprecationWarning) for w in caught)
    assert str(err) == "legacy message"
    assert err.operator == ""
    assert err.__cause__ is None
