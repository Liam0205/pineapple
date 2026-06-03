"""Template-string utilities for runtime parameter interpolation (issue #74).

The Apple DSL supports per-request `{{field_name}}` interpolation in two places:

1. `if_() / elseif_()` control predicates — extraction and Lua emission live
   in `apple/control.py`.
2. Operator params declared as `templatable` in their codegen schema. When a
   user passes a string value containing `{{...}}` for such a param, the
   compiler auto-injects the referenced common fields into the operator's
   common_input, and the runtime resolves the values per request via the
   TemplatedParamsAware hook.

The shared regex + extraction primitives live in ``apple/_template_syntax``
(issue #76 unification). This module exposes the param-path public API.
"""
from __future__ import annotations

from typing import Any

from apple._template_syntax import (
    TEMPLATE_PATTERN,
    extract_fields,
    is_bare_template,
)

__all__ = [
    "extract_fields",
    "extract_fields_from_params",
    "is_bare_template",
    "is_templated",
]


def is_templated(value: Any) -> bool:
    """Return True iff ``value`` is a string containing at least one ``{{field}}`` marker."""
    return isinstance(value, str) and bool(TEMPLATE_PATTERN.search(value))


def extract_fields_from_params(params: dict[str, Any]) -> list[str]:
    """Walk a param dict and return ordered, deduped fields referenced anywhere.

    Only the top-level string values are scanned. Templating is undefined for
    nested objects / lists per the scalar-only contract (issue #74).
    """
    seen: dict[str, None] = {}
    for v in params.values():
        if is_templated(v):
            for f in extract_fields(v):
                seen.setdefault(f, None)
    return list(seen.keys())
