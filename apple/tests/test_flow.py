"""Tests for Flow and SubFlow composition."""
import json
import pytest
import sys
import os

# Ensure apple package is importable
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple.flow import Flow, SubFlow
from apple.validator import ValidationError
from apple._version import __version__


class TestBasicFlow:
    def test_simple_flow_compiles(self):
        flow = Flow(
            name="test",
            common_input=["user_id"],
            item_output=["item_score"],
        )
        flow.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
        )
        cfg = flow.compile_dict()
        assert "_PINEAPPLE_VERSION" in cfg
        assert "pipeline_config" in cfg
        assert "pipeline_group" in cfg
        assert "flow_contract" in cfg
        assert cfg["flow_contract"]["common_input"] == ["user_id"]

    def test_operator_chain(self):
        flow = Flow(
            name="chain",
            common_input=["scene"],
            item_input=["item_id", "item_score", "item_status"],
            item_output=["item_score"],
        )
        flow.filter_condition(
            item_input=["item_status"],
            item_output=["item_status", "item_score"],
            field="item_status", value="offline",
        ).reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        )
        cfg = flow.compile_dict()
        ops = cfg["pipeline_config"]["operators"]
        assert len(ops) == 2
        # Check that both operators are present
        type_names = [op["type_name"] for op in ops.values()]
        assert "filter_condition" in type_names
        assert "reorder_sort" in type_names

    def test_unique_names(self):
        flow = Flow(name="names", item_input=["x"], item_output=["y", "z"])
        flow._add_op("transform_by_lua", item_input=["x"], item_output=["y"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="")
        flow._add_op("transform_by_lua", item_input=["y"], item_output=["z"],
                      lua_script="function g() return y end",
                      function_for_item="g", function_for_common="")
        cfg = flow.compile_dict()
        names = list(cfg["pipeline_config"]["operators"].keys())
        assert len(names) == 2
        assert names[0] != names[1]  # unique


class TestSubFlow:
    def test_subflow_composition(self):
        sub1 = SubFlow(name="recall_stage")
        sub1.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
        )

        sub2 = SubFlow(name="rank_stage")
        sub2.reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        )

        flow = Flow(
            name="full",
            item_output=["item_id", "item_score"],
            sub_flows=[sub1, sub2],
        )
        cfg = flow.compile_dict()
        ops = cfg["pipeline_config"]["operators"]
        assert len(ops) == 2
        # pipeline_map should have two sub-flows
        pmap = cfg["pipeline_config"]["pipeline_map"]
        assert "recall_stage" in pmap
        assert "rank_stage" in pmap


class TestJsonOutput:
    def test_json_roundtrip(self):
        flow = Flow(
            name="json_test",
            common_input=["user_age"],
            item_output=["item_adjusted"],
        )
        flow._add_op("recall_static",
                      item_output=["item_id", "item_price"],
                      items=[])
        flow._add_op("transform_by_lua",
                      common_input=["user_age"],
                      item_input=["item_price"],
                      item_output=["item_adjusted"],
                      lua_script="function f() return item_price end",
                      function_for_item="f", function_for_common="")

        json_str = flow.compile()
        parsed = json.loads(json_str)
        assert parsed["_PINEAPPLE_VERSION"] == __version__
        assert "operators" in parsed["pipeline_config"]

    def test_metadata_structure(self):
        flow = Flow(
            name="meta",
            common_input=["age"],
            item_output=["result"],
        )
        flow._add_op("transform_by_lua",
                      common_input=["age"],
                      item_output=["result"],
                      lua_script="function f() return age end",
                      function_for_common="f", function_for_item="")

        cfg = flow.compile_dict()
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        meta = op["$metadata"]
        assert meta["common_input"] == ["age"]
        assert meta["item_output"] == ["result"]
