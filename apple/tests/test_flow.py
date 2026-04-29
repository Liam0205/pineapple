"""Tests for Flow and SubFlow composition."""
import json
import os
import sys

# Ensure apple package is importable
sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple._version import __version__
from apple.flow import Flow, SubFlow


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

    def test_invalid_storage_mode_rejected(self):
        import pytest
        with pytest.raises(ValueError, match="invalid storage_mode='raw'"):
            Flow(name="bad", storage_mode="raw")

    def test_valid_storage_modes_accepted(self):
        Flow(name="ok_row", storage_mode="row")
        Flow(name="ok_col", storage_mode="column")
        Flow(name="ok_none")


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

    def test_nested_subflow(self):
        inner = SubFlow(name="candidates")
        inner.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
        )

        outer = SubFlow(name="recall")
        outer.add_subflow(inner)
        outer.filter_condition(
            item_input=["item_score"],
            item_output=["item_score"],
            field="item_score", value=0,
        )

        flow = Flow(
            name="nested",
            item_output=["item_id", "item_score"],
            sub_flows=[outer],
        )
        cfg = flow.compile_dict()
        pmap = cfg["pipeline_config"]["pipeline_map"]
        assert "recall" in pmap
        assert "recall/candidates" in pmap

        # pipeline_group should reference "recall" directly
        group = cfg["pipeline_group"]["main"]["pipeline"]
        assert "recall" in group

        # recall pipeline contains candidates subflow ref + filter op
        recall_entries = pmap["recall"]["pipeline"]
        assert "recall/candidates" in recall_entries
        assert len(recall_entries) == 2

    def test_mixed_ops_and_subflows(self):
        sub = SubFlow(name="inner")
        sub.reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        )

        flow = Flow(name="mix", item_output=["item_id", "item_score"])
        flow.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
        )
        flow.add_subflow(sub)

        cfg = flow.compile_dict()
        group = cfg["pipeline_group"]["main"]["pipeline"]
        # First entry should be the direct op, second the subflow ref
        assert len(group) == 2
        assert group[1] == "inner"

    def test_add_subflow_slash_rejected(self):
        import pytest
        sf = SubFlow(name="a/b")
        flow = Flow(name="test")
        with pytest.raises(ValueError, match="must not contain '/'"):
            flow.add_subflow(sf)

    def test_subflow_inside_if_branch(self):
        sf = SubFlow(name="ranking")
        sf.reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        )
        flow = Flow(
            name="test",
            common_input=["enabled"],
            item_input=["item_score"],
            item_output=["item_score"],
        )
        flow.if_("{{enabled}}").add_subflow(sf).end_if_()
        cfg = flow.compile_dict()
        ops = cfg["pipeline_config"]["operators"]
        sort_op = next(
            op for op in ops.values()
            if op["type_name"] == "reorder_sort"
        )
        assert sort_op["skip"] == ["_if_1"]
        assert "_if_1" in sort_op["$metadata"]["common_input"]

    def test_nested_control_inside_branch_subflow(self):
        sf = SubFlow(name="inner")
        sf.if_("{{flag}}").reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        ).end_if_()

        flow = Flow(
            name="test",
            common_input=["enabled", "flag"],
            item_input=["item_score"],
            item_output=["item_score"],
        )
        flow.if_("{{enabled}}").add_subflow(sf).end_if_()
        cfg = flow.compile_dict()
        ops = cfg["pipeline_config"]["operators"]
        sort_op = next(
            op for op in ops.values()
            if op["type_name"] == "reorder_sort"
        )
        # Should have both the inner (renamed) skip and the outer skip
        assert "_if_1" in sort_op["skip"]  # outer flow's if
        assert any("_inner_" in s for s in sort_op["skip"])  # inner SubFlow's renamed if
        assert len(sort_op["skip"]) == 2

    def test_subflow_if_else_both_branches(self):
        sf1 = SubFlow(name="branch_a")
        sf1.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
        )
        sf2 = SubFlow(name="branch_b")
        sf2.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "b", "item_score": 2.0}],
        )
        flow = Flow(
            name="test",
            common_input=["mode"],
            item_output=["item_id", "item_score"],
        )
        flow.if_("{{mode}} == 1") \
            .add_subflow(sf1) \
        .else_() \
            .add_subflow(sf2) \
        .end_if_()
        cfg = flow.compile_dict()
        ops = cfg["pipeline_config"]["operators"]
        recall_ops = [
            op for op in ops.values()
            if op["type_name"] == "recall_static"
        ]
        assert len(recall_ops) == 2
        skip_fields = [tuple(op["skip"]) for op in recall_ops]
        # One should have _if_1, the other _else_2
        all_skips = {s for t in skip_fields for s in t}
        assert any(s.startswith("_if_") for s in all_skips)
        assert any(s.startswith("_else_") for s in all_skips)

    def test_compile_subflow_control_is_idempotent(self):
        sf = SubFlow(name="inner")
        sf.if_("{{flag}}").reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        ).end_if_()

        flow = Flow(
            name="test",
            common_input=["flag"],
            item_input=["item_score"],
            item_output=["item_score"],
        )
        flow.add_subflow(sf)

        cfg1 = flow.compile_dict()
        cfg2 = flow.compile_dict()
        assert cfg1["pipeline_config"] == cfg2["pipeline_config"]
        assert cfg1["pipeline_group"] == cfg2["pipeline_group"]

    def test_subflow_under_internal_control_uses_renamed_parent_skip(self):
        child = SubFlow(name="child")
        child.reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        )

        parent = SubFlow(name="parent")
        parent.if_("{{enabled}}").add_subflow(child).end_if_()

        flow = Flow(
            name="test",
            common_input=["enabled"],
            item_input=["item_score"],
            item_output=["item_score"],
        )
        flow.add_subflow(parent)

        cfg = flow.compile_dict()
        ops = cfg["pipeline_config"]["operators"]
        ctrl_op = next(op for op in ops.values() if op.get("for_branch_control"))
        sort_op = next(
            op for op in ops.values()
            if op["type_name"] == "reorder_sort"
        )

        renamed_field = ctrl_op["$metadata"]["common_output"][0]
        assert renamed_field == "_parent_if_1"
        assert sort_op["skip"] == [renamed_field]
        assert renamed_field in sort_op["$metadata"]["common_input"]
        assert "_if_1" not in sort_op["skip"]

    def test_subflow_cycle_detected(self):
        import pytest

        from apple.validator import ValidationError
        sf = SubFlow(name="self_ref")
        sf._sub_flows.append(sf)
        sf._child_order.append(("sf", 0))

        flow = Flow(
            name="cycle_test",
            item_output=["item_score"],
        )
        flow.add_subflow(sf)
        flow.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
        )
        with pytest.raises(ValidationError, match="cycle or reuse"):
            flow.compile_dict()

    def test_add_subflow_chaining(self):
        sub1 = SubFlow(name="a")
        sub1.recall_static(
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
        )
        sub2 = SubFlow(name="b")
        sub2.reorder_sort(
            item_input=["item_score"],
            field="item_score", order="desc",
        )

        flow = Flow(name="chain", item_output=["item_id", "item_score"])
        flow.add_subflow(sub1).add_subflow(sub2)

        cfg = flow.compile_dict()
        pmap = cfg["pipeline_config"]["pipeline_map"]
        assert "a" in pmap
        assert "b" in pmap


class TestTypedOperators:
    def test_baseop_apply_inside_control_branch_gets_skip(self):
        from apple.base import BaseOp

        flow = Flow(
            name="typed_control",
            common_input=["enabled"],
            item_input=["item_score"],
            item_output=["item_score"],
        )
        op = BaseOp(flow)
        op._name = "reorder_sort"

        flow.if_("{{enabled}}")
        op._apply(
            params={"field": "item_score", "order": "desc"},
            item_input=["item_score"],
        )
        flow.end_if_()

        cfg = flow.compile_dict()
        sort_op = next(
            op
            for op in cfg["pipeline_config"]["operators"].values()
            if op["type_name"] == "reorder_sort"
        )
        assert sort_op["skip"] == ["_if_1"]
        assert sort_op["$metadata"]["common_input"][0] == "_if_1"

    def test_baseop_apply_inside_nested_control_gets_all_skips(self):
        from apple.base import BaseOp

        flow = Flow(
            name="typed_nested_control",
            common_input=["enabled", "ready"],
            item_input=["item_score"],
            item_output=["item_score"],
        )
        op = BaseOp(flow)
        op._name = "reorder_sort"

        flow.if_("{{enabled}}").if_("{{ready}}")
        op._apply(
            params={"field": "item_score", "order": "desc"},
            item_input=["item_score"],
        )
        flow.end_if_().end_if_()

        cfg = flow.compile_dict()
        sort_op = next(
            op
            for op in cfg["pipeline_config"]["operators"].values()
            if op["type_name"] == "reorder_sort"
        )
        assert sort_op["skip"] == ["_if_1", "_if_2"]


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
