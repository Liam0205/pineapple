class ConfigError(Exception):
    pass


class RegistryError(RuntimeError):
    pass


class ValidationError(ValueError):
    pass


class OperatorException(Exception):
    pass


class ExecutionError(RuntimeError):
    def __init__(self, operator: str, message: str):
        super().__init__(f'operator "{operator}": {message}')
        self.operator = operator


class PanicError(RuntimeError):
    def __init__(self, message: str, detail: str = ""):
        super().__init__(message)
        self.detail = detail

    def detailed_error(self) -> str:
        if self.detail:
            return f"{self}: {self.detail}"
        return str(self)
