"""Shared `{{field}}` syntax primitives for the Apple compiler (issue #76).

Both `apple/control.py` (if_/elseif_ predicates) and `apple/template.py`
(operator-param interpolation) need to recognize the same `{{field}}` marker.
This module centralizes the regex and the two extraction primitives so the
two consumers stay byte-identical without coupling their downstream paths
(Lua emission vs. pre-Execute value substitution).

Private module: no public API surface — callers re-export selectively.
"""
from __future__ import annotations

import re
from typing import Any

TEMPLATE_PATTERN = re.compile(r"\{\{(\w+)\}\}")
BARE_TEMPLATE_PATTERN = re.compile(r"^\{\{(\w+)\}\}$")


def extract_fields(value: str) -> list[str]:
    """Return ordered, deduped field names referenced inside ``{{...}}`` markers."""
    return list(dict.fromkeys(TEMPLATE_PATTERN.findall(value)))


def is_bare_template(value: Any) -> bool:
    """Return True iff ``value`` is exactly ``{{field}}`` with no surrounding text."""
    return isinstance(value, str) and bool(BARE_TEMPLATE_PATTERN.fullmatch(value))
