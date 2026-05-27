"""Tests for the compiler: JSON output format, operator naming, control flow lowering."""
import os
import sys

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple.compiler import compile_flow
from apple.flow import Flow, SubFlow


class TestControlFlowLowering:
    def test_simple_if(self):
        flow = Flow(
            name="if_test",
            common_input=["item_count"],
            item_input=["item_score"],
            item_output=["item_rank"],
        )
        flow.if_("{{item_count}} > 0") \
            .reorder_sort(
                item_input=["item_score"],
                item_output=["item_rank"],
                order="desc",
            ) \
        .end_if_()

        cfg = compile_flow(flow)
        ops = cfg["pipeline_config"]["operators"]

        # Should have 2 operators: control + sort
        assert len(ops) == 2

        # Find control operator
        ctrl_ops = {n: o for n, o in ops.items() if o.get("for_branch_control")}
        assert len(ctrl_ops) == 1
        ctrl_name, ctrl_op = list(ctrl_ops.items())[0]
        assert ctrl_op["type_name"] == "transform_by_lua"
        assert "_if_1" in ctrl_op["$metadata"]["common_output"]
        assert "function evaluate()" in ctrl_op["lua_script"]

        # Find sort operator — should have skip field
        sort_ops = {n: o for n, o in ops.items() if o["type_name"] == "reorder_sort"}
        assert len(sort_ops) == 1
        sort_op = list(sort_ops.values())[0]
        assert sort_op["skip"] == ["_if_1"]

    def test_if_elseif_else(self):
        flow = Flow(
            name="branches",
            common_input=["mode"],
            item_input=["item_score"],
            item_output=["item_rank", "item_fallback", "item_default"],
        )
        flow.if_("{{mode}} == 1") \
            .reorder_sort(
                item_input=["item_score"],
                item_output=["item_rank"],
                order="desc",
            ) \
        .elseif_("{{mode}} == 2") \
            ._add_op("transform_by_lua",
                item_input=["item_score"],
                item_output=["item_fallback"],
                lua_script="function f() return item_score * 0.5 end",
                function_for_item="f", function_for_common="") \
        .else_() \
            ._add_op("transform_by_lua",
                item_input=["item_score"],
                item_output=["item_default"],
                lua_script="function g() return 0 end",
                function_for_item="g", function_for_common="") \
        .end_if_()

        cfg = compile_flow(flow)
        ops = cfg["pipeline_config"]["operators"]

        # 3 control ops + 3 business ops = 6
        assert len(ops) == 6

        ctrl_ops = [o for o in ops.values() if o.get("for_branch_control")]
        assert len(ctrl_ops) == 3

        # Check skip fields on business ops
        biz_ops = [o for o in ops.values() if not o.get("for_branch_control")]
        all_skip_fields = [s for o in biz_ops for s in (o.get("skip") or [])]
        assert "_if_1" in all_skip_fields
        assert any("_elif_" in s for s in all_skip_fields)
        assert any("_else_" in s for s in all_skip_fields)

    def test_nested_if(self):
        flow = Flow(
            name="nested",
            common_input=["a", "b"],
            item_input=["x"],
            item_output=["y", "z"],
        )
        flow.if_("{{a}} > 0") \
            ._add_op("transform_by_lua", item_input=["x"], item_output=["y"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="") \
            .if_("{{b}} > 0") \
                ._add_op("transform_by_lua", item_input=["x"], item_output=["z"],
                          lua_script="function g() return x * 2 end",
                          function_for_item="g", function_for_common="") \
            .end_if_() \
        .end_if_()

        cfg = compile_flow(flow)
        ops = cfg["pipeline_config"]["operators"]
        # 2 control ops + 2 business ops = 4
        assert len(ops) == 4
        ctrl_by_output = {
            op["$metadata"]["common_output"][0]: op
            for op in ops.values()
            if op.get("for_branch_control")
        }
        assert ctrl_by_output["_if_2"]["skip"] == ["_if_1"]
        assert ctrl_by_output["_if_2"]["$metadata"]["common_input"][0] == "_if_1"

        outer_op = next(
            op for op in ops.values()
            if op["$metadata"]["item_output"] == ["y"]
        )
        inner_op = next(
            op for op in ops.values()
            if op["$metadata"]["item_output"] == ["z"]
        )
        assert outer_op["skip"] == ["_if_1"]
        assert inner_op["skip"] == ["_if_1", "_if_2"]

    def test_if_with_string_literal(self):
        """String literals in if_ condition must not be treated as field names."""
        flow = Flow(
            name="str_lit",
            common_input=["group_value"],
            item_input=["x"],
            item_output=["y"],
        )
        flow.if_('{{group_value}} == "treatment"') \
            ._add_op("transform_by_lua", item_input=["x"], item_output=["y"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="") \
        .end_if_()

        cfg = compile_flow(flow)
        ops = cfg["pipeline_config"]["operators"]
        assert len(ops) == 2

        ctrl_op = [o for o in ops.values() if o.get("for_branch_control")][0]
        assert "treatment" not in ctrl_op["$metadata"]["common_input"]
        assert "group_value" in ctrl_op["$metadata"]["common_input"]


class TestExtractFields:
    def test_extracts_template_fields(self):
        from apple.control import extract_fields
        assert extract_fields("{{item_count}} > 0") == ["item_count"]

    def test_ignores_bare_identifiers(self):
        from apple.control import extract_fields
        assert extract_fields("item_count > 0") == []

    def test_string_literals_not_extracted(self):
        from apple.control import extract_fields
        assert extract_fields('{{group_value}} == "treatment"') == ["group_value"]

    def test_multiple_fields(self):
        from apple.control import extract_fields
        assert extract_fields("{{a}} == 1 and {{b}} ~= nil") == ["a", "b"]

    def test_deduplicates(self):
        from apple.control import extract_fields
        assert extract_fields("{{x}} > 0 and {{x}} < 10") == ["x"]


class TestOperatorNaming:
    def test_names_are_unique(self):
        flow = Flow(name="naming", item_input=["x"], item_output=["a", "b"])
        # Each op reads x and writes a unique output; later ops read previous output
        flow._add_op("transform_by_lua", item_input=["x"], item_output=["a"],
                      lua_script="function f0() return x end",
                      function_for_item="f0", function_for_common="")
        flow._add_op("transform_by_lua", item_input=["a"], item_output=["a"],
                      lua_script="function f1() return a end",
                      function_for_item="f1", function_for_common="")
        flow._add_op("transform_by_lua", item_input=["a"], item_output=["a"],
                      lua_script="function f2() return a end",
                      function_for_item="f2", function_for_common="")
        flow._add_op("transform_by_lua", item_input=["a"], item_output=["b"],
                      lua_script="function f3() return a end",
                      function_for_item="f3", function_for_common="")
        flow._add_op("transform_by_lua", item_input=["b"], item_output=["b"],
                      lua_script="function f4() return b end",
                      function_for_item="f4", function_for_common="")
        cfg = compile_flow(flow)
        names = list(cfg["pipeline_config"]["operators"].keys())
        assert len(names) == 5
        assert len(names) == len(set(names))

    def test_name_format(self):
        flow = Flow(name="fmt", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        name = list(cfg["pipeline_config"]["operators"].keys())[0]
        assert name.startswith("transform_by_lua_")
        assert len(name.split("_")) >= 2

    def test_explicit_name(self):
        """Explicit name= appears as JSON operator key."""
        flow = Flow(name="exp", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", name="my_step",
                      common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        names = list(cfg["pipeline_config"]["operators"].keys())
        assert "my_step" in names

    def test_explicit_name_via_getattr(self):
        """Explicit name= works through flow.op(...) dynamic dispatch."""
        flow = Flow(name="ga", common_input=["x"], common_output=["y"])
        flow.transform_by_lua(name="custom_lua",
                 common_input=["x"], common_output=["y"],
                 lua_script="function f() return x end",
                 function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        names = list(cfg["pipeline_config"]["operators"].keys())
        assert "custom_lua" in names

    def test_duplicate_explicit_name_raises(self):
        """Two operators with the same explicit name must fail."""
        from apple.validator import ValidationError
        flow = Flow(name="dup", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", name="same_name",
                      common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow._add_op("transform_by_lua", name="same_name",
                      common_input=["y"], common_output=["y"],
                      lua_script="function g() return y end",
                      function_for_common="g", function_for_item="")
        with pytest.raises(ValidationError, match="duplicate explicit operator name"):
            compile_flow(flow)

    def test_mixed_explicit_and_auto(self):
        """Mix of explicit and auto-generated names in a single flow."""
        flow = Flow(name="mix", item_input=["x"], item_output=["a", "b"])
        flow._add_op("transform_by_lua", name="step_one",
                      item_input=["x"], item_output=["a"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="")
        flow._add_op("transform_by_lua", item_input=["a"], item_output=["b"],
                      lua_script="function g() return a end",
                      function_for_item="g", function_for_common="")
        cfg = compile_flow(flow)
        names = list(cfg["pipeline_config"]["operators"].keys())
        assert len(names) == 2
        assert "step_one" in names
        auto_name = [n for n in names if n != "step_one"][0]
        assert auto_name.startswith("transform_by_lua_")

    def test_hash_collision_appends_suffix(self):
        """When two auto-named ops produce the same hash, compiler appends _N."""
        from unittest.mock import patch

        class FakeMD5:
            def __init__(self):
                pass
            def hexdigest(self):
                return "aabbcc000000"
            def update(self, data):
                pass

        flow = Flow(name="collision", item_input=["x"], item_output=["a", "b"])
        flow._add_op("transform_by_lua", item_input=["x"], item_output=["a"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="")
        flow._add_op("transform_by_lua", item_input=["a"], item_output=["b"],
                      lua_script="function g() return a end",
                      function_for_item="g", function_for_common="")

        with patch("hashlib.md5", return_value=FakeMD5()):
            cfg = compile_flow(flow)

        names = list(cfg["pipeline_config"]["operators"].keys())
        assert len(names) == 2
        assert names[0] == "transform_by_lua_AABBCC"
        assert names[1] == "transform_by_lua_AABBCC_1"
        assert len(set(names)) == 2


class TestPipelineMap:
    def test_subflow_pipeline_map(self):
        sub = SubFlow(name="my_stage")
        sub._add_op("transform_by_lua", item_input=["x"], item_output=["y"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="")

        flow = Flow(name="test", item_input=["x"], item_output=["y"],
                    sub_flows=[sub])
        cfg = compile_flow(flow)
        pmap = cfg["pipeline_config"]["pipeline_map"]
        assert "my_stage" in pmap
        assert len(pmap["my_stage"]["pipeline"]) == 1

    def test_pipeline_group_references_map(self):
        flow = Flow(name="test", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        group_pipelines = cfg["pipeline_group"]["main"]["pipeline"]
        pmap_keys = list(cfg["pipeline_config"]["pipeline_map"].keys())
        # No subflows → pipeline_map is empty, group lists operators directly
        assert pmap_keys == []
        assert len(group_pipelines) == 1
        assert group_pipelines[0].startswith("transform_by_lua_")


class TestDefaultsAndDebug:
    def test_common_defaults_in_json(self):
        flow = Flow(name="defaults_test", common_input=["age"], common_output=["result"])
        flow._add_op("transform_by_lua",
                      common_input=["age"],
                      common_output=["result"],
                      common_defaults={"age": 25},
                      lua_script="function f() return age end",
                      function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert op["common_defaults"] == {"age": 25}

    def test_item_defaults_in_json(self):
        flow = Flow(
            name="item_def", common_input=["age"],
            item_input=["price"], item_output=["result"],
        )
        flow._add_op("transform_by_lua",
                      common_input=["age"],
                      item_input=["price"],
                      item_output=["result"],
                      item_defaults={"price": 0.0},
                      lua_script="function f() return price end",
                      function_for_item="f", function_for_common="")
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert op["item_defaults"] == {"price": 0.0}

    def test_debug_flag_in_json(self):
        flow = Flow(name="debug_test", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua",
                      common_input=["x"],
                      common_output=["y"],
                      debug=True,
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert op["debug"] is True

    def test_no_defaults_no_debug_omitted(self):
        flow = Flow(name="clean", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua",
                      common_input=["x"],
                      common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert "common_defaults" not in op
        assert "item_defaults" not in op
        assert "debug" not in op


class TestCodeInfo:
    def test_code_info_present(self):
        flow = Flow(name="codeinfo", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua",
                      common_input=["x"],
                      common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert "$code_info" in op
        code_info = op["$code_info"]
        assert "test_compiler.py" in code_info
        assert ".transform_by_lua(...)" in code_info

    def test_code_info_via_getattr(self):
        flow = Flow(name="ci2", common_input=["x"], common_output=["y"])
        flow.transform_by_lua(
            common_input=["x"],
            common_output=["y"],
            lua_script="function f() return x end",
            function_for_common="f", function_for_item="")
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert "$code_info" in op
        assert ".transform_by_lua(...)" in op["$code_info"]


class TestLogPrefix:
    def test_log_prefix_in_json(self):
        flow = Flow(
            name="lp_test",
            common_input=["x"],
            common_output=["y"],
            log_prefix="[svc] ",
        )
        flow._add_op(
            "transform_by_lua",
            common_input=["x"],
            common_output=["y"],
            lua_script="function f() return x end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        assert cfg["log_prefix"] == "[svc] "

    def test_no_log_prefix_omitted(self):
        flow = Flow(name="no_lp", common_input=["x"], common_output=["y"])
        flow._add_op(
            "transform_by_lua",
            common_input=["x"],
            common_output=["y"],
            lua_script="function f() return x end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        assert "log_prefix" not in cfg


class TestGlobalDebug:
    def test_debug_true_in_json(self):
        flow = Flow(
            name="dbg_test",
            common_input=["x"],
            common_output=["y"],
            debug=True,
        )
        flow._add_op(
            "transform_by_lua",
            common_input=["x"],
            common_output=["y"],
            lua_script="function f() return x end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        assert cfg["debug"] is True

    def test_debug_false_omitted(self):
        flow = Flow(name="no_dbg", common_input=["x"], common_output=["y"])
        flow._add_op(
            "transform_by_lua",
            common_input=["x"],
            common_output=["y"],
            lua_script="function f() return x end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        assert "debug" not in cfg


class TestCompileIdempotency:
    def test_nested_subflow_control_flow_idempotent(self):
        """Compiling the same Flow twice must produce identical output."""
        inner = SubFlow(name="inner")
        inner.if_("{{flag}} > 0") \
            ._add_op("transform_by_lua", item_input=["x"], item_output=["y"],
                     lua_script="function f() return x end",
                     function_for_item="f", function_for_common="") \
        .end_if_()

        flow = Flow(
            name="idempotent_test",
            common_input=["flag"],
            item_input=["x"],
            item_output=["y"],
            sub_flows=[inner],
        )

        cfg1 = compile_flow(flow)
        cfg2 = compile_flow(flow)
        # Remove timestamps for comparison
        cfg1.pop("_PINEAPPLE_CREATE_TIME", None)
        cfg2.pop("_PINEAPPLE_CREATE_TIME", None)
        assert cfg1 == cfg2


class TestRenameControlFields:
    def test_empty_path_no_rename(self):
        """Top-level ops (empty path) should not be renamed."""
        from apple.base import OpCall
        from apple.compiler import _rename_control_fields

        op = OpCall(
            type_name="transform_by_lua",
            params={},
            common_output=["_if_1"],
            for_branch_control=True,
            name="if_1",
        )
        renames = _rename_control_fields([op], [("op", 0)], "")
        assert renames == {}
        assert op.common_output == ["_if_1"]

    def test_subflow_path_renames_control_field(self):
        """Control op in a SubFlow should have its field prefixed."""
        from apple.base import OpCall
        from apple.compiler import _rename_control_fields

        ctrl_op = OpCall(
            type_name="transform_by_lua",
            params={},
            common_output=["_if_1"],
            for_branch_control=True,
            name="if_1",
        )
        biz_op = OpCall(
            type_name="noop",
            params={},
            common_input=["_if_1"],
            skip=["_if_1"],
        )
        renames = _rename_control_fields(
            [ctrl_op, biz_op], [("op", 0), ("op", 1)], "L1"
        )
        assert "_if_1" in renames
        assert renames["_if_1"] == "_L1::if_1"
        assert ctrl_op.common_output == ["_L1::if_1"]
        assert biz_op.common_input == ["_L1::if_1"]
        assert biz_op.skip == ["_L1::if_1"]

    def test_subflow_rename_updates_lua_script(self):
        """Lua evaluate in else/elseif must reference namespaced field via _ENV."""
        from apple.base import OpCall
        from apple.compiler import _rename_control_fields

        lua_if = (
            "function evaluate() if (flag == true) "
            "then return false else return true end end"
        )
        lua_else = (
            "function evaluate() if ((_if_1)) "
            "then return false else return true end end"
        )

        ctrl_if = OpCall(
            type_name="transform_by_lua",
            params={
                "lua_script": lua_if,
                "function_for_common": "evaluate",
                "function_for_item": "",
            },
            common_output=["_if_1"],
            for_branch_control=True,
            name="if_1",
        )
        ctrl_else = OpCall(
            type_name="transform_by_lua",
            params={
                "lua_script": lua_else,
                "function_for_common": "evaluate",
                "function_for_item": "",
            },
            common_input=["_if_1"],
            common_output=["_else_2"],
            for_branch_control=True,
            name="else_2",
        )
        _rename_control_fields(
            [ctrl_if, ctrl_else], [("op", 0), ("op", 1)], "my_sf"
        )
        assert '_ENV["_my_sf::if_1"]' in ctrl_else.params["lua_script"]
        assert "(_if_1)" not in ctrl_else.params["lua_script"]

    def test_nested_path_uses_double_colon(self):
        """Nested SubFlow path uses :: as separator."""
        from apple.base import OpCall
        from apple.compiler import _rename_control_fields

        ctrl_op = OpCall(
            type_name="transform_by_lua",
            params={},
            common_output=["_if_1"],
            for_branch_control=True,
            name="if_1",
        )
        renames = _rename_control_fields(
            [ctrl_op], [("op", 0)], "L1/L2"
        )
        assert renames["_if_1"] == "_L1::L2::if_1"


class TestInjectInheritedSkips:
    def test_empty_inherited_no_change(self):
        """No inherited skips → ops unchanged."""
        from apple.base import OpCall
        from apple.compiler import _inject_inherited_skips

        op = OpCall(type_name="noop", params={}, common_input=["x"], skip=[])
        _inject_inherited_skips([op], [("op", 0)], [])
        assert op.skip == []
        assert op.common_input == ["x"]

    def test_injects_skip_and_common_input(self):
        """Inherited skips are appended to skip and prepended to common_input."""
        from apple.base import OpCall
        from apple.compiler import _inject_inherited_skips

        op = OpCall(type_name="noop", params={}, common_input=["x"], skip=[])
        _inject_inherited_skips([op], [("op", 0)], ["_if_1"])
        assert "_if_1" in op.skip
        assert op.common_input[0] == "_if_1"

    def test_no_duplicate_injection(self):
        """If skip field already present, do not duplicate."""
        from apple.base import OpCall
        from apple.compiler import _inject_inherited_skips

        op = OpCall(
            type_name="noop", params={},
            common_input=["_if_1", "x"],
            skip=["_if_1"],
        )
        _inject_inherited_skips([op], [("op", 0)], ["_if_1"])
        assert op.skip.count("_if_1") == 1
        assert op.common_input.count("_if_1") == 1

    def test_skips_sf_entries(self):
        """SubFlow entries in child_order are ignored."""
        from apple.base import OpCall
        from apple.compiler import _inject_inherited_skips

        op = OpCall(type_name="noop", params={}, common_input=[], skip=[])
        _inject_inherited_skips([op], [("op", 0), ("sf", 1)], ["_if_1"])
        assert "_if_1" in op.skip


class TestCollectExclusionGroups:
    def test_no_closed_blocks(self):
        """Node without _closed_blocks should not add groups."""
        from apple.compiler import _collect_exclusion_groups

        class FakeNode:
            pass

        node = FakeNode()
        groups: list = []
        _collect_exclusion_groups(node, {}, groups)
        assert groups == []

    def test_collects_renamed_fields(self):
        """Exclusion groups use renamed field names."""
        from apple.compiler import _collect_exclusion_groups
        from apple.control import ControlBlock, ControlBranch

        class FakeNode:
            _closed_blocks = [
                ControlBlock(
                    block_id=1,
                    branches=[
                        ControlBranch(
                            kind="if", condition="x",
                            ctrl_field="_if_1", ctrl_index=1,
                        ),
                        ControlBranch(
                            kind="else", condition=None,
                            ctrl_field="_else_2", ctrl_index=2,
                        ),
                    ],
                    closed=True,
                )
            ]

        node = FakeNode()
        groups: list = []
        renames = {"_if_1": "_L1::if_1"}
        _collect_exclusion_groups(node, renames, groups)
        assert len(groups) == 1
        assert "_L1::if_1" in groups[0]
        assert "_else_2" in groups[0]
