"""Template-string utilities for runtime parameter interpolation (issue #74).

The Apple DSL supports per-request `{{field_name}}` interpolation in two places:

1. `if_() / elseif_()` control predicates — extraction and Lua emission live
   in `apple/control.py`. That mechanism is unchanged.
2. Operator params declared as `templatable` in their codegen schema. When a
   user passes a string value containing `{{...}}` for such a param, the
   compiler auto-injects the referenced common fields into the operator's
   common_input, and the runtime resolves the values per request via the
   TemplatedParamsAware hook.

This module is the shared helper layer for case (2). It is intentionally
distinct from `apple/control.py` for now — see issue #74 discussion — but we
expect to unify the two paths in a follow-up once the operator-param path
has stabilized.
"""
from __future__ import annotations

import re
from typing import Any

_TEMPLATE_PATTERN = re.compile(r"\{\{(\w+)\}\}")
_BARE_TEMPLATE_PATTERN = re.compile(r"^\{\{(\w+)\}\}$")


def is_templated(value: Any) -> bool:
    """Return True iff ``value`` is a string containing at least one ``{{field}}`` marker."""
    return isinstance(value, str) and bool(_TEMPLATE_PATTERN.search(value))


def is_bare_template(value: Any) -> bool:
    """Return True iff ``value`` is exactly ``{{field}}`` with no surrounding text.

    L0 contract: a templated param value must consist of a single bare
    ``{{field}}`` marker — no literal prefix, suffix, or multiple markers.
    Mixed forms like ``"prefix_{{x}}"`` are rejected so the model stays
    "the template binds a common-field value to a named param" rather than
    "the template builds a string by concatenation".
    """
    return isinstance(value, str) and bool(_BARE_TEMPLATE_PATTERN.fullmatch(value))


def extract_fields(value: str) -> list[str]:
    """Return ordered, deduped field names referenced inside ``{{...}}`` markers."""
    return list(dict.fromkeys(_TEMPLATE_PATTERN.findall(value)))


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
