"""Unit tests for pine.dag module."""
from __future__ import annotations

import pytest
from pine.config import Metadata, OperatorConfig
from pine.dag import DAG
from pine.errors import ConfigError


def _transform(
    item_in: list[str] | None = None,
    item_out: list[str] | None = None,
    sources: list[str] | None = None,
) -> OperatorConfig:
    cfg = OperatorConfig()
    cfg.type_name = "test"
    cfg.metadata = Metadata()
    cfg.metadata.item_input = item_in or []
    cfg.metadata.item_output = item_out or []
    cfg.metadata.common_input = []
    cfg.metadata.common_output = []
    cfg.sources = sources or []
    return cfg


def _recall(item_out: list[str] | None = None) -> OperatorConfig:
    cfg = OperatorConfig()
    cfg.type_name = "test"
    cfg.recall = True
    cfg.additive_writes_row_set = True
    cfg.metadata = Metadata()
    cfg.metadata.item_input = []
    cfg.metadata.item_output = item_out or []
    cfg.metadata.common_input = []
    cfg.metadata.common_output = []
    cfg.sources = []
    return cfg


def _filter(item_in: list[str] | None = None) -> OperatorConfig:
    cfg = OperatorConfig()
    cfg.type_name = "test"
    cfg.consumes_row_set = True
    cfg.mutates_row_set = True
    cfg.metadata = Metadata()
    cfg.metadata.item_input = item_in or []
    cfg.metadata.item_output = []
    cfg.metadata.common_input = []
    cfg.metadata.common_output = []
    cfg.sources = []
    return cfg


class TestBuildCycleMaskedByReduce:
    """Regression: fuzz found that reduce() before cycle detection can mask
    cycles.  When sources create backwards edges forming a cycle, reduce may
    remove "redundant" cycle edges, causing topological sort to miss the cycle.
    """

    def test_cycle_detected(self):
        seq = ["op_0", "op_1", "op_2", "op_3"]
        ops = {
            "op_0": _transform(item_in=["a"], sources=["op_3"]),
            "op_1": _filter(item_in=["b"]),
            "op_2": _transform(item_in=["a", "b"], sources=["op_0"]),
            "op_3": _recall(item_out=["a"]),
        }

        with pytest.raises(ConfigError, match="cycle"):
            DAG.build(seq, ops, {})
