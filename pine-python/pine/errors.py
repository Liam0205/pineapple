import warnings


class ConfigError(Exception):
    pass


class RegistryError(RuntimeError):
    pass


class ValidationError(ValueError):
    pass


class OperatorException(Exception):
    pass


class ExecutionError(RuntimeError):
    """Raised when an operator hits a fatal error during DAG execution.

    Modern form: ``ExecutionError(operator, cause)``.
    When ``cause`` is a ``BaseException`` it is stored on ``__cause__``
    so downstream code can do ``isinstance(err.__cause__, SomeType)`` —
    mirrors Go ``errors.As`` / Java ``Throwable.getCause()`` / pine-cpp
    ``std::nested_exception``.

    Legacy form: ``ExecutionError("message text")``. Continues to work
    so we don't break downstream user code that pip-installs the
    package, but emits a ``DeprecationWarning`` pointing at the new
    signature. The single-arg form sets ``operator=""`` and treats the
    string as the full message body.

    The serialized ``args[0]`` form keeps the ``operator "X": <message>``
    shape on the two-arg path, so cross-validate Section 5 (error-parity)
    still passes byte-exact.
    """

    def __init__(self, *args, **kwargs):
        # Two-arg form ⇒ canonical.
        if len(args) == 2 and not kwargs:
            operator, cause = args
            if isinstance(cause, BaseException):
                super().__init__(f'operator "{operator}": {cause}')
                self.__cause__ = cause
            else:
                super().__init__(f'operator "{operator}": {cause}')
            self.operator = operator
            return
        # One-arg form ⇒ legacy; warn and adapt.
        if len(args) == 1 and not kwargs:
            warnings.warn(
                "ExecutionError(message) is deprecated; use "
                "ExecutionError(operator, cause) so cause chains survive "
                "(see pine.errors docs / issue #34).",
                DeprecationWarning,
                stacklevel=2,
            )
            super().__init__(str(args[0]))
            self.operator = ""
            return
        raise TypeError(
            "ExecutionError requires (operator, cause) or legacy (message); "
            f"got {len(args)} positional args, kwargs={list(kwargs)}"
        )


class PanicError(RuntimeError):
    """Raised when an operator throws an unexpected (non-pine) exception.

    Accepts an optional BaseException ``cause`` keyword. When supplied, it
    is stored on ``__cause__`` so the original exception remains
    introspectable from downstream code, mirroring pine-cpp's
    ``std::nested_exception`` and pine-go's ``Unwrap``.
    """

    def __init__(self, message: str, detail: str = "", cause=None):
        super().__init__(message)
        self.detail = detail
        if isinstance(cause, BaseException):
            self.__cause__ = cause

    def detailed_error(self) -> str:
        if self.detail:
            return f"{self}: {self.detail}"
        return str(self)
