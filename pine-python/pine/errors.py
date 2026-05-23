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

    Accepts either a message string (legacy) or a BaseException as cause.
    When a BaseException is passed, it is stored as ``__cause__`` so that
    downstream ``isinstance(err.__cause__, SomeType)`` checks (and
    ``traceback.print_exception``'s "The above exception was the direct
    cause of...") behave like Go ``errors.As`` / Java ``Throwable.getCause()``.

    The serialized ``args[0]`` form stays byte-exact with the legacy
    ``operator "X": <message>`` shape, so cross-validate Section 5
    (error-parity) keeps passing.
    """

    def __init__(self, operator: str, cause):
        if isinstance(cause, BaseException):
            super().__init__(f'operator "{operator}": {cause}')
            self.__cause__ = cause
        else:
            super().__init__(f'operator "{operator}": {cause}')
        self.operator = operator


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
