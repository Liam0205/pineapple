from __future__ import annotations

from typing import Any, Callable

from pine.config import RESERVED_KEYS  # noqa: F401 — re-export for backwards compat
from pine.errors import RegistryError
from pine.operator import Operator, OperatorParams, OperatorSchema


class Registry:
    """Operator type registry mapping type names to schemas and factories."""

    _global: "Registry | None" = None

    def __init__(self):
        self._schemas: dict[str, OperatorSchema] = {}
        self._factories: dict[str, Callable[[], Operator]] = {}

    @classmethod
    def global_instance(cls) -> "Registry":
        assert cls._global is not None, "Registry not initialized (circular import?)"
        return cls._global

    @classmethod
    def register_global(cls, schema: OperatorSchema, factory: Callable[[], Operator]):
        cls.global_instance().register(schema, factory)

    def register(self, schema: OperatorSchema, factory: Callable[[], Operator]):
        if not schema.name:
            raise RegistryError("operator schema name is empty")
        if not schema.description:
            raise RegistryError(f"operator \"{schema.name}\": description is empty")
        if schema.name in self._schemas:
            raise RegistryError(f"operator \"{schema.name}\" already registered")
        self._schemas[schema.name] = schema
        self._factories[schema.name] = factory

    def build_operator(self, type_name: str, raw_params: dict[str, Any]) -> Operator:
        schema = self._schemas.get(type_name)
        if schema is None:
            raise RegistryError(f"operator type not registered: \"{type_name}\"")
        factory = self._factories[type_name]

        params = self._validate_and_extract_params(schema, raw_params)
        op = factory()
        try:
            op.init(OperatorParams(params))
        except RegistryError:
            raise
        except Exception as e:
            raise RegistryError(f'operator "{type_name}": init failed: {e}') from e
        return op

    def _validate_and_extract_params(
        self, schema: OperatorSchema, raw_params: dict[str, Any]
    ) -> dict[str, Any]:
        params: dict[str, Any] = {}
        for key, value in raw_params.items():
            if key in RESERVED_KEYS:
                continue
            if key not in schema.params:
                raise RegistryError(
                    f"unknown parameter \"{key}\" for operator \"{schema.name}\""
                )
            params[key] = value

        for name, spec in schema.params.items():
            if name not in params:
                if spec.required:
                    raise RegistryError(
                        f"required parameter \"{name}\" missing for operator \"{schema.name}\""
                    )
                if spec.default_value is not None:
                    params[name] = spec.default_value

        return params

    def schemas(self) -> list[OperatorSchema]:
        return list(self._schemas.values())

    def has(self, type_name: str) -> bool:
        return type_name in self._schemas

    def get_schema(self, type_name: str) -> "OperatorSchema | None":
        return self._schemas.get(type_name)


Registry._global = Registry()
