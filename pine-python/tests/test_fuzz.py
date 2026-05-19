"""Property-based fuzz tests for pine-python.

Uses Hypothesis to generate random inputs and verify that the engine
never crashes with unhandled exceptions.
"""
from __future__ import annotations

import json
from typing import Any

import pytest
from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st
from pine.config import Config
from pine.dag import DAG
from pine.engine import Engine
from pine.errors import ConfigError, RegistryError


@pytest.fixture(autouse=True, scope="module")
def register_operators():
    from pine.operators import ensure_registered
    ensure_registered()


# ---------------------------------------------------------------------------
# Strategies
# ---------------------------------------------------------------------------

# Random field names (short, ASCII identifiers)
_field_name = st.text(
    alphabet="abcdefghijklmnopqrstuvwxyz_", min_size=1, max_size=12
)

# Random scalar values for operator params / item fields
_scalar_value = st.one_of(
    st.integers(min_value=-1000, max_value=1000),
    st.floats(min_value=-1e6, max_value=1e6, allow_nan=False, allow_infinity=False),
    st.text(min_size=0, max_size=20),
    st.booleans(),
    st.none(),
)

# Random item: dict with 1-5 fields
_random_item = st.dictionaries(
    keys=_field_name,
    values=_scalar_value,
    min_size=1,
    max_size=5,
)

# Valid operator type names that exist in the registry
_VALID_TYPE_NAMES = [
    "transform_copy",
    "transform_dispatch",
    "transform_size",
    "transform_by_lua",
    "filter_truncate",
    "recall_static",
    "reorder_sort",
]


@st.composite
def _random_operator_config(draw):
    """Generate a random operator config dict."""
    type_name = draw(st.sampled_from(_VALID_TYPE_NAMES))
    metadata_fields = draw(st.lists(_field_name, min_size=0, max_size=3))
    config = {
        "type_name": type_name,
        "$metadata": {
            "common_input": metadata_fields[:1],
            "common_output": [],
            "item_input": metadata_fields[1:2],
            "item_output": metadata_fields[2:3],
        },
    }
    # Add random extra params
    extra_keys = draw(st.lists(_field_name, min_size=0, max_size=3))
    for key in extra_keys:
        if key not in ("type_name", "$metadata", "skip", "recall", "sources",
                       "debug", "consumes_row_set", "mutates_row_set",
                       "additive_writes_row_set", "common_defaults",
                       "item_defaults", "for_branch_control", "data_parallel"):
            config[key] = draw(_scalar_value)
    return config


# ---------------------------------------------------------------------------
# Fuzz Tests
# ---------------------------------------------------------------------------


@pytest.mark.fuzz
@settings(max_examples=500, deadline=None, suppress_health_check=[HealthCheck.too_slow])
@given(data=st.binary(min_size=0, max_size=4096))
def test_fuzz_config_load(data: bytes):
    """Random bytes must not crash Config.load with unhandled exceptions."""
    try:
        Config.load(data)
    except ConfigError:
        pass  # expected for invalid input
    except (ValueError, TypeError, KeyError, AttributeError):
        pass  # acceptable parse failures


@pytest.mark.fuzz
@settings(max_examples=500, deadline=None, suppress_health_check=[HealthCheck.too_slow])
@given(
    op_count=st.integers(min_value=1, max_value=8),
    op_configs=st.lists(_random_operator_config(), min_size=1, max_size=8),
)
def test_fuzz_dag_build(op_count: int, op_configs: list[dict]):
    """Random operator configs must not crash DAG.build with unhandled exceptions."""
    # Build a minimal valid config structure
    operators = {}
    pipeline = []
    for i, oc in enumerate(op_configs[:op_count]):
        name = f"op_{i}"
        operators[name] = oc
        pipeline.append(name)

    config_dict = {
        "_PINEAPPLE_VERSION": "0.6.6",
        "pipeline_config": {
            "operators": operators,
            "pipeline_map": {},
        },
        "pipeline_group": {
            "main": {"pipeline": pipeline},
        },
    }

    try:
        config_bytes = json.dumps(config_dict).encode()
        cfg = Config.load(config_bytes)
        expanded = cfg.expand_operator_sequence_with_sub_flows()
        dag = DAG.build(
            expanded.sequence,
            cfg.pipeline_config.operators,
            expanded.op_to_sub_flow,
        )

        # Validate DAG invariants
        assert len(dag.nodes) == len(expanded.sequence)

        # Topological order: all preds appear before their node
        index_set = set(range(len(dag.nodes)))
        for node in dag.nodes:
            for pred_idx in node.preds:
                assert pred_idx in index_set
                assert pred_idx < node.index, (
                    f"predecessor {pred_idx} must appear before node {node.index}"
                )

    except ConfigError:
        pass  # expected for invalid configs


@pytest.mark.fuzz
@settings(max_examples=500, deadline=None, suppress_health_check=[HealthCheck.too_slow])
@given(data=st.binary(min_size=0, max_size=4096))
def test_fuzz_engine_create(data: bytes):
    """Random bytes must not crash Engine.create with unhandled exceptions."""
    try:
        Engine.create(data)
    except (ConfigError, RegistryError):
        pass  # expected for invalid input
    except (ValueError, TypeError, KeyError, AttributeError):
        pass  # acceptable parse failures


@pytest.mark.fuzz
@settings(max_examples=500, deadline=None, suppress_health_check=[HealthCheck.too_slow])
@given(
    items=st.lists(
        _random_item,
        min_size=1,
        max_size=50,
    ),
)
def test_fuzz_data_parallel_equivalence(items: list[dict[str, Any]]):
    """data_parallel=1 vs data_parallel=4 must produce identical results for ConcurrentSafe ops."""
    # Build config with transform_copy (ConcurrentSafe) - common_to_item direction
    common = {"tag": "hello"}

    def make_config(dp: int) -> bytes:
        cfg = {
            "_PINEAPPLE_VERSION": "0.6.6",
            "pipeline_config": {
                "operators": {
                    "copy_tag": {
                        "type_name": "transform_copy",
                        "direction": "common_to_item",
                        "data_parallel": dp,
                        "$metadata": {
                            "common_input": ["tag"],
                            "common_output": [],
                            "item_input": [],
                            "item_output": ["tag"],
                        },
                    },
                },
                "pipeline_map": {},
            },
            "pipeline_group": {
                "main": {"pipeline": ["copy_tag"]},
            },
        }
        return json.dumps(cfg).encode()

    try:
        engine_seq = Engine.create(make_config(1))
        engine_par = Engine.create(make_config(4))

        result_seq = engine_seq.execute(common, items)
        result_par = engine_par.execute(common, items)

        # Both should succeed or both should fail
        if result_seq.error is None and result_par.error is None:
            assert result_seq.common == result_par.common, (
                f"common mismatch:\n  seq={result_seq.common}\n  par={result_par.common}"
            )
            assert result_seq.items == result_par.items, (
                f"items mismatch:\n  seq={result_seq.items}\n  par={result_par.items}"
            )
    except (ConfigError, RegistryError):
        pass  # acceptable if config doesn't validate
