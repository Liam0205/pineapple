"""Reverse differential checks for pine-go/testdata/e2e_*.json fixtures.

The fixtures are loaded directly by Go integration tests via ``loadConfig``
and never compared against a regenerated DSL output, so a schema change in
the Apple compiler can leave them silently stale — the Go engine still
loads them (it tolerates missing fields via registry fallback) and tests
keep passing on outdated assertions.

This module pulls in the opposite direction:

1. **Shape drift** (every fixture) — every ``type_name`` in the fixture
   must exist in the current operator registry mirror, marker bools that
   appear inline must match ``apple_generated/markers.py``, and the
   pipeline version must equal ``apple._version.__version__``.

2. **DSL round-trip drift** (fixtures with a known DSL source) — recompile
   the same DSL with the current Apple compiler, normalise both sides
   (strip ``_PINEAPPLE_CREATE_TIME``/``$code_info``, rename hash-suffixed
   op keys to a stable form), and assert deep equality. A schema change
   in the compiler that the fixture has not absorbed fails the diff
   loudly.
"""
from __future__ import annotations

import json
import os
import re
import sys
from typing import Any

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple._version import __version__
from apple.flow import Flow
from apple_generated.markers import OPERATOR_MARKERS

TESTDATA_DIR = os.path.join(
    os.path.dirname(__file__), "..", "..", "pine-go", "testdata"
)

ALL_FIXTURES = sorted(
    f for f in os.listdir(TESTDATA_DIR)
    if f.startswith("e2e_") and f.endswith(".json")
)

MARKER_KEYS = ("consumes_row_set", "mutates_row_set", "additive_writes_row_set")

# Operators registered dynamically inside Go integration tests (init blocks)
# rather than under pine-go/operators/. They are intentionally absent from
# Apple's codegen-emitted markers table — registry membership is enforced
# at engine load time on the Go side.
TEST_ONLY_OPERATORS = frozenset({
    "transform_test_resource_read",
})


def _load(name: str) -> dict[str, Any]:
    with open(os.path.join(TESTDATA_DIR, name)) as f:
        return json.load(f)


class TestFixtureShapeDrift:
    """Per-operator schema invariants that every fixture must hold."""

    @pytest.mark.parametrize("fixture", ALL_FIXTURES)
    def test_version_matches_current(self, fixture):
        cfg = _load(fixture)
        assert cfg.get("_PINEAPPLE_VERSION") == __version__, (
            f"{fixture}: _PINEAPPLE_VERSION={cfg.get('_PINEAPPLE_VERSION')!r} "
            f"diverges from apple._version.__version__={__version__!r}. "
            f"Regenerate the fixture or roll the version bump through it."
        )

    @pytest.mark.parametrize("fixture", ALL_FIXTURES)
    def test_operator_types_are_registered(self, fixture):
        cfg = _load(fixture)
        ops = cfg["pipeline_config"]["operators"]
        for op_name, op in ops.items():
            type_name = op["type_name"]
            if type_name in TEST_ONLY_OPERATORS:
                continue
            assert type_name in OPERATOR_MARKERS, (
                f"{fixture}::{op_name}: type_name={type_name!r} is not in "
                f"the codegen-emitted markers table. Either the operator "
                f"was renamed/removed, codegen was not re-run, or it is a "
                f"test-only operator that should be added to "
                f"TEST_ONLY_OPERATORS."
            )

    @pytest.mark.parametrize("fixture", ALL_FIXTURES)
    def test_inline_markers_agree_with_registry(self, fixture):
        """Fixture marker bools must match markers.py.

        Two directions of drift are caught:

        * **Mismatch** — if the fixture spells out a marker, the value must
          equal the registry mirror.
        * **Missing True** — if the registry says a marker is True, the
          fixture must spell it out explicitly. A missing key would be
          treated as False by JSON loaders that default-zero unset bools,
          so silent omission of a True marker is a real semantic drift.

        Missing False markers stay tolerated: the absent-key default is
        False everywhere, so omission is observationally equivalent.
        """
        cfg = _load(fixture)
        ops = cfg["pipeline_config"]["operators"]
        mismatches: list[str] = []
        for op_name, op in ops.items():
            type_name = op["type_name"]
            if type_name in TEST_ONLY_OPERATORS:
                continue
            expected = OPERATOR_MARKERS.get(type_name, {})
            for key in MARKER_KEYS:
                want = expected.get(key, False)
                if key in op:
                    got = op[key]
                    if want != got:
                        mismatches.append(
                            f"{op_name}({type_name}).{key}: fixture={got!r} "
                            f"vs markers.py={want!r}"
                        )
                elif want:
                    mismatches.append(
                        f"{op_name}({type_name}).{key}: missing from fixture "
                        f"but markers.py says True (silent drift — fixture "
                        f"would load as False)"
                    )
        assert not mismatches, (
            f"{fixture}: marker drift between fixture and codegen mirror:\n  "
            + "\n  ".join(mismatches)
        )

    @pytest.mark.parametrize("fixture", ALL_FIXTURES)
    def test_metadata_block_is_complete(self, fixture):
        cfg = _load(fixture)
        ops = cfg["pipeline_config"]["operators"]
        required = {"common_input", "common_output", "item_input", "item_output"}
        for op_name, op in ops.items():
            meta = op.get("$metadata")
            assert isinstance(meta, dict), (
                f"{fixture}::{op_name}: $metadata is missing or not a dict"
            )
            missing = required - meta.keys()
            assert not missing, (
                f"{fixture}::{op_name}: $metadata missing keys {sorted(missing)}"
            )


# --- DSL round-trip ---


_HASH_KEY_RE = re.compile(r"^([a-z][a-z0-9_]*)_[0-9A-F]{6}$")


def _normalise(cfg: dict[str, Any]) -> dict[str, Any]:
    """Drop runtime-volatile fields and rewrite hash-suffixed op keys.

    ``$code_info`` paths and ``_PINEAPPLE_CREATE_TIME`` are caller-environment
    dependent, and ``unique_name`` hashes change whenever any field included
    in the hash input changes — so we rename ``recall_static_AB12CD`` to
    ``recall_static__0`` based on first-appearance order in the operators
    map. Pipeline references are remapped consistently.
    """
    cfg = json.loads(json.dumps(cfg))  # deep copy
    cfg.pop("_PINEAPPLE_CREATE_TIME", None)

    ops = cfg["pipeline_config"]["operators"]
    rename: dict[str, str] = {}
    type_counters: dict[str, int] = {}
    for op_name in ops:
        m = _HASH_KEY_RE.match(op_name)
        if m:
            type_name = m.group(1)
            idx = type_counters.get(type_name, 0)
            rename[op_name] = f"{type_name}__{idx}"
            type_counters[type_name] = idx + 1

    # Apply rename across operators dict + every pipeline reference.
    new_ops: dict[str, Any] = {}
    for old_name, op in ops.items():
        op.pop("$code_info", None)
        new_ops[rename.get(old_name, old_name)] = op
    cfg["pipeline_config"]["operators"] = new_ops

    pmap = cfg["pipeline_config"].get("pipeline_map", {})
    for path, body in pmap.items():
        body["pipeline"] = [rename.get(x, x) for x in body.get("pipeline", [])]

    pgroup = cfg.get("pipeline_group", {})
    for name, body in pgroup.items():
        body["pipeline"] = [rename.get(x, x) for x in body.get("pipeline", [])]

    return cfg


def _build_e2e_apple_dsl_flow() -> Flow:
    """Replicate the DSL declared in test_e2e.py::test_go_engine_executes_dsl_json.

    Kept in lockstep with the test that owns this fixture. If the test is
    edited, this builder must be edited too — the round-trip diff makes
    that explicit.
    """
    flow = Flow(
        name="apple_e2e",
        common_input=["user_age"],
        item_output=["item_id", "item_final_price"],
    )
    flow._add_op(
        "recall_static",
        item_output=["item_id", "item_price"],
        items=[
            {"item_id": "a", "item_price": 100.0},
            {"item_id": "b", "item_price": 200.0},
            {"item_id": "c", "item_price": 50.0},
        ],
    )
    flow._add_op(
        "transform_by_lua",
        common_input=["user_age"],
        item_input=["item_price"],
        item_output=["item_final_price"],
        lua_script=(
            "function f()\n"
            "  if user_age < 18 then\n"
            "    return item_price * 0.8\n"
            "  else\n"
            "    return item_price\n"
            "  end\n"
            "end"
        ),
        function_for_item="f",
        function_for_common="",
    )
    flow.reorder_sort(item_input=["item_final_price"], order="desc")
    return flow


# Map fixture file -> builder function. Only fixtures with a known DSL
# source belong here; others are covered by the shape-drift tests above.
DSL_SOURCED_FIXTURES = {
    "e2e_apple_dsl.json": _build_e2e_apple_dsl_flow,
}


class TestDSLRoundTripDrift:
    """For every fixture with a known DSL source, the current compiler must
    produce a normalised JSON that deep-equals the checked-in fixture."""

    @pytest.mark.parametrize("fixture", sorted(DSL_SOURCED_FIXTURES))
    def test_recompile_matches_fixture(self, fixture):
        flow = DSL_SOURCED_FIXTURES[fixture]()
        actual = json.loads(flow.compile())

        expected = _load(fixture)

        norm_actual = _normalise(actual)
        norm_expected = _normalise(expected)

        assert norm_actual == norm_expected, (
            f"{fixture}: recompiling its DSL source produces a different "
            f"config than the checked-in fixture. The fixture is stale — "
            f"regenerate it (or revert the compiler change)."
        )
