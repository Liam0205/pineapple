# Changelog — pine-python

All notable changes to the published `pine` package (PyPI / `pip install pine`)
land here. Format loosely follows [Keep a Changelog](https://keepachangelog.com).

## Unreleased

### Changed

- **`ExecutionError.__init__` signature evolution (issue #34, P1-E5 in code
  review).** The modern canonical form is `ExecutionError(operator, cause)`;
  when `cause` is a `BaseException` it is stored on `__cause__` so downstream
  code can do `isinstance(err.__cause__, RedisError)` (parity with Go
  `errors.As` / Java `Throwable.getCause()` / pine-cpp `std::nested_exception`).
  The legacy single-arg form `ExecutionError("just a message")` continues to
  work for backwards compatibility but emits a `DeprecationWarning` pointing
  at the new signature. The single-arg path sets `operator=""` and uses the
  string verbatim as the message.

- **`PanicError.__init__` accepts optional `cause` kwarg.** When supplied,
  set on `__cause__`. Same intent: preserve the original exception for
  structured downstream identification.

### Behavior

- `engine.py` operator-failure path now threads the original exception
  through `PanicError(cause=exec_err)` so the cause chain survives the
  `_set_fatal` wrap step.
