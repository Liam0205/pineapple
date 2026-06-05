"""Guard against reintroducing pine-cpp's pre-ResourceManager redis API.

Before commit ``2a88df3`` (feat(pine-cpp): migrate Redis operators to
ResourceManager connection pools), pine-cpp's redis path was driven by
``RedisParams`` + a ``shared_pool()`` singleton. The migration removed
both — connection pooling now flows through ``ResourceManager`` and the
``redis_connection`` resource type.

There is no compile-time barrier preventing the legacy names from
sneaking back in (e.g. via a copy-paste from an old branch, an external
contribution, or a partial revert). This test scans the runtime source
trees for the two legacy identifiers and fails loudly if either
reappears anywhere outside known-historical locations.

Audit reference: ``L2`` in v0.9.0..HEAD test coverage audit.
"""
from __future__ import annotations

import os
import re

REPO_ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), "..", ".."))

# Runtime source roots to scan. Build artefacts, third-party deps, and
# the `.code-review/` historical audit notes are deliberately excluded.
SCAN_ROOTS = [
    os.path.join(REPO_ROOT, "pine-cpp", "include"),
    os.path.join(REPO_ROOT, "pine-cpp", "src"),
    os.path.join(REPO_ROOT, "pine-cpp", "tests"),
    os.path.join(REPO_ROOT, "pine-go"),
    os.path.join(REPO_ROOT, "pine-java", "src"),
    os.path.join(REPO_ROOT, "apple"),
    os.path.join(REPO_ROOT, "apple_generated"),
]

SCAN_EXTENSIONS = (".cpp", ".hpp", ".h", ".cc", ".go", ".java", ".py")

# Files explicitly allowed to mention the legacy symbols. This guard file is
# the only intentional reference site — it has to spell the names out to scan
# for them.
SELF_PATH = os.path.normpath(os.path.abspath(__file__))

LEGACY_PATTERNS = (
    re.compile(r"\bRedisParams\b"),
    re.compile(r"\bshared_pool\b"),
)

# Excluded directory components anywhere in the path.
EXCLUDED_PARTS = {
    "build", "third_party", "_deps", "target", "node_modules",
    "__pycache__", ".pytest_cache",
}


def _iter_source_files():
    for root in SCAN_ROOTS:
        if not os.path.isdir(root):
            continue
        for dirpath, dirnames, filenames in os.walk(root):
            dirnames[:] = [d for d in dirnames if d not in EXCLUDED_PARTS]
            for name in filenames:
                if not name.endswith(SCAN_EXTENSIONS):
                    continue
                full = os.path.normpath(os.path.abspath(os.path.join(dirpath, name)))
                if full == SELF_PATH:
                    continue
                yield full


def test_legacy_redis_symbols_not_reintroduced():
    """No production source under SCAN_ROOTS may mention RedisParams or
    shared_pool. The migration to ResourceManager removed both; reintroduction
    would break the resource_config hot-reload path and the cross-runtime
    metrics-name parity (cache → cache_v2 swap in `10-hot-reload.sh`)."""
    hits: list[str] = []
    for path in _iter_source_files():
        try:
            with open(path, encoding="utf-8") as f:
                text = f.read()
        except (OSError, UnicodeDecodeError):
            continue
        for line_no, line in enumerate(text.splitlines(), start=1):
            for pat in LEGACY_PATTERNS:
                m = pat.search(line)
                if m:
                    rel = os.path.relpath(path, REPO_ROOT)
                    hits.append(f"{rel}:{line_no}: {m.group(0)} -> {line.strip()}")
    assert not hits, (
        "Legacy redis API names re-appeared in runtime source. The pine-cpp "
        "migration to ResourceManager removed RedisParams + shared_pool; "
        "all redis pooling now flows through redis_connection resources.\n  "
        + "\n  ".join(hits)
    )
