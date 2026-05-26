"""Fixture-based test runner for pine-python.

Runs operator fixtures from ../fixtures/operators/ and pipeline fixtures
from ../fixtures/pipelines/ against the Python engine, comparing output
with expected results.
"""
from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import pytest

FIXTURES_ROOT = Path(__file__).parent.parent.parent / "fixtures"
OPERATOR_FIXTURES = sorted(FIXTURES_ROOT.glob("operators/*.json"))
PIPELINE_FIXTURES = sorted(FIXTURES_ROOT.glob("pipelines/*.json"))
ERROR_FIXTURES = sorted(FIXTURES_ROOT.glob("errors/*.json"))


def normalize_value(v: Any) -> Any:
    if isinstance(v, dict):
        return {k: normalize_value(val) for k, val in v.items()}
    if isinstance(v, list):
        return [normalize_value(x) for x in v]
    if isinstance(v, (int, float)):
        if isinstance(v, bool):
            return v
        return float(v)
    return v


# Machine epsilon for IEEE 754 double precision: 2^-52
_FLOAT_EPSILON = 2**-52


def _floats_equal(a: float, b: float) -> bool:
    """Compare two floats using relative epsilon.

    Formula: |a - b| <= eps * max(|a|, |b|, 1.0)
    """
    diff = abs(a - b)
    scale = max(abs(a), abs(b), 1.0)
    return diff <= _FLOAT_EPSILON * scale


def values_equal(a: Any, b: Any) -> bool:
    na = normalize_value(a)
    nb = normalize_value(b)
    return _values_equal_impl(na, nb)


def _values_equal_impl(a: Any, b: Any) -> bool:
    if isinstance(a, float) and isinstance(b, float):
        return _floats_equal(a, b)
    if isinstance(a, dict) and isinstance(b, dict):
        if a.keys() != b.keys():
            return False
        return all(_values_equal_impl(a[k], b[k]) for k in a)
    if isinstance(a, list) and isinstance(b, list):
        if len(a) != len(b):
            return False
        return all(_values_equal_impl(x, y) for x, y in zip(a, b))
    return a == b


def _run_operator_directly(operator_name: str, params: dict, metadata: dict,
                           input_data: dict, static_resources: dict | None = None):
    """Run an operator directly and return its OperatorOutput."""
    from pine.cancellation import CancellationToken
    from pine.operator import MetadataAware, OperatorInput, OperatorOutput, ResourceAware
    from pine.registry import Registry

    op = Registry.global_instance().build_operator(operator_name, params)

    if isinstance(op, MetadataAware):
        op.set_metadata(
            metadata.get("common_input", []),
            metadata.get("common_output", []),
            metadata.get("item_input", []),
            metadata.get("item_output", []),
        )

    if isinstance(op, ResourceAware) and static_resources:
        from pine.engine import StaticResourceProvider
        op.set_resource_provider(StaticResourceProvider(static_resources))

    common = input_data.get("common", {})
    items = input_data.get("items", [])
    input_ = OperatorInput(common, items)
    output = OperatorOutput()
    token = CancellationToken()

    op.execute(token, input_, output)
    return output


@pytest.fixture(autouse=True)
def register_operators():
    from pine.operators import ensure_registered
    ensure_registered()


class TestOperatorFixtures:
    @pytest.mark.parametrize(
        "fixture_path",
        OPERATOR_FIXTURES,
        ids=[p.stem for p in OPERATOR_FIXTURES],
    )
    def test_operator_fixture(self, fixture_path: Path):
        data = json.loads(fixture_path.read_text())
        operator_name = data["operator"]
        cases = data.get("cases", [])
        static_resources = data.get("static_resources")

        for i, case in enumerate(cases):
            name = case.get("name", f"case_{i}")
            params = case.get("params", {})
            metadata = case.get("metadata", {})
            input_data = case["input"]
            expected = case["expected"]

            output = _run_operator_directly(
                operator_name, params, metadata, input_data, static_resources
            )

            if "removed_indices" in expected:
                got = sorted(output.removed_items)
                exp = sorted(expected["removed_indices"])
                assert got == exp, (
                    f"{operator_name}/{name}: removed_indices mismatch\n"
                    f"  got:      {got}\n"
                    f"  expected: {exp}"
                )

            if "added_items" in expected:
                assert values_equal(output.added_items, expected["added_items"]), (
                    f"{operator_name}/{name}: added_items mismatch\n"
                    f"  got:      {output.added_items}\n"
                    f"  expected: {expected['added_items']}"
                )

            if "item_order" in expected:
                assert output.item_order == expected["item_order"], (
                    f"{operator_name}/{name}: item_order mismatch\n"
                    f"  got:      {output.item_order}\n"
                    f"  expected: {expected['item_order']}"
                )

            if "items" in expected:
                got_items: list[dict] = []
                exp_items = expected["items"]
                for idx in range(len(exp_items)):
                    got_items.append(output.item_writes.get(idx, {}))
                assert values_equal(got_items, exp_items), (
                    f"{operator_name}/{name}: items mismatch\n"
                    f"  got:      {got_items}\n"
                    f"  expected: {exp_items}"
                )

            if "common" in expected:
                assert values_equal(output.common_writes, expected["common"]), (
                    f"{operator_name}/{name}: common mismatch\n"
                    f"  got:      {output.common_writes}\n"
                    f"  expected: {expected['common']}"
                )


class TestPipelineFixtures:
    @pytest.mark.parametrize(
        "fixture_path",
        PIPELINE_FIXTURES,
        ids=[p.stem for p in PIPELINE_FIXTURES],
    )
    def test_pipeline_fixture(self, fixture_path: Path):
        from pine.engine import Engine, StaticResourceProvider

        data = json.loads(fixture_path.read_text())
        requires = data.get("requires", [])
        if requires:
            pytest.skip(f"requires {requires}")
        config = data.get("config", data)
        cases = data.get("cases", [])
        if not cases:
            pytest.skip("no cases in fixture")

        static_resources = data.get("static_resources")
        config_bytes = json.dumps(config).encode()

        rp = StaticResourceProvider(static_resources) if static_resources else None
        engine = Engine.create(config_bytes, resource_provider=rp)

        for i, case in enumerate(cases):
            name = case.get("name", f"case_{i}")
            request = case.get("request", {})
            expected = case.get("expected", {})
            expect_error = case.get("expect_error", "")

            common = request.get("common", {})
            items = request.get("items", [])

            result = engine.execute(common, items)

            if expect_error:
                assert result.error is not None, (
                    f"{fixture_path.stem}/{name}: expected error containing "
                    f"{expect_error!r}, got success"
                )
                assert expect_error in str(result.error), (
                    f"{fixture_path.stem}/{name}: expected error containing "
                    f"{expect_error!r}, got: {result.error}"
                )
                continue

            assert result.error is None, (
                f"{fixture_path.stem}/{name}: execution error: {result.error}"
            )
            assert values_equal(result.common, expected.get("common", {})), (
                f"{fixture_path.stem}/{name}: common mismatch\n"
                f"  got:      {result.common}\n"
                f"  expected: {expected.get('common', {})}"
            )
            assert values_equal(result.items, expected.get("items", [])), (
                f"{fixture_path.stem}/{name}: items mismatch\n"
                f"  got:      {json.dumps(result.items, indent=2)}\n"
                f"  expected: {json.dumps(expected.get('items', []), indent=2)}"
            )


class TestErrorFixtures:
    @pytest.mark.parametrize(
        "fixture_path",
        ERROR_FIXTURES,
        ids=[p.stem for p in ERROR_FIXTURES],
    )
    def test_error_fixture(self, fixture_path: Path):
        from pine.engine import Engine
        from pine.errors import (
            ConfigError,
            OperatorException,
            PanicError,
            RegistryError,
            ValidationError,
        )

        data = json.loads(fixture_path.read_text())
        config = data.get("config", data)
        expected_error = data.get("expected_error", {})
        if isinstance(expected_error, str):
            expected_error = {"type": "ConfigError", "message_contains": expected_error}

        error_type = expected_error.get("type", "ConfigError")
        message_contains = expected_error.get("message_contains", "")

        error_class_map = {
            "ConfigError": ConfigError,
            "ValidationError": ValidationError,
            "RegistryError": RegistryError,
            "OperatorException": OperatorException,
            "PanicError": PanicError,
            "ExecutionError": Exception,
        }
        error_class = error_class_map.get(error_type, Exception)

        config_bytes = json.dumps(config).encode()
        request = data.get("request")

        # Build resource provider from resource_config if present
        rp = None
        rc = config.get("resource_config")
        if rc:
            from pine.engine import StaticResourceProvider
            sr = {k: v.get("params", {}).get("value") for k, v in rc.items()}
            rp = StaticResourceProvider(sr)

        if error_type == "ExecutionError":
            engine = Engine.create(config_bytes, resource_provider=rp)
            req_common = request.get("common", {}) if request else {}
            req_items = request.get("items", []) if request else []
            result = engine.execute(req_common, req_items)
            assert result.error is not None, (
                f"{fixture_path.stem}: expected execution error but got None"
            )
            assert message_contains in str(result.error), (
                f"{fixture_path.stem}: error message mismatch\n"
                f"  got:      {result.error}\n"
                f"  expected to contain: {message_contains}"
            )
        elif request is not None and error_type not in ("ConfigError", "RegistryError"):
            engine = Engine.create(config_bytes, resource_provider=rp)
            req_common = request.get("common", {}) if request else {}
            req_items = request.get("items", []) if request else []
            with pytest.raises(error_class) as exc_info:
                engine.execute(req_common, req_items)
            assert message_contains in str(exc_info.value), (
                f"{fixture_path.stem}: error message mismatch\n"
                f"  got:      {exc_info.value}\n"
                f"  expected to contain: {message_contains}"
            )
        else:
            with pytest.raises(error_class) as exc_info:
                Engine.create(config_bytes, resource_provider=rp)

            assert message_contains in str(exc_info.value), (
                f"{fixture_path.stem}: error message mismatch\n"
                f"  got:      {exc_info.value}\n"
                f"  expected to contain: {message_contains}"
            )
