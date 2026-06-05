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
        # After #74: skip dependencies land in common_input_skip, not common_input.
        assert ctrl_by_output["_if_2"]["$metadata"]["common_input_skip"] == ["_if_1"]

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


class TestStrictFields:
    def test_strict_common_in_json(self):
        flow = Flow(
            name="strict_test",
            common_input=["age", "name"],
            common_output=["result"],
        )
        flow._add_op(
            "transform_by_lua",
            common_input=["age", "name"],
            common_output=["result"],
            strict_common=["age"],
            lua_script="function f() return age end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert op["strict_common"] == ["age"]
        assert "nullable_common" not in op

    def test_strict_item_in_json(self):
        flow = Flow(
            name="strict_item_test",
            item_input=["price"],
            item_output=["result"],
        )
        flow._add_op(
            "transform_by_lua",
            item_input=["price"],
            item_output=["result"],
            strict_item=["price"],
            lua_script="function f() return price end",
            function_for_item="f",
            function_for_common="",
        )
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert op["strict_item"] == ["price"]
        assert "nullable_item" not in op

    def test_no_strict_omitted(self):
        flow = Flow(name="no_strict", common_input=["x"], common_output=["y"])
        flow._add_op(
            "transform_by_lua",
            common_input=["x"],
            common_output=["y"],
            lua_script="function f() return x end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert "strict_common" not in op
        assert "strict_item" not in op
        assert "nullable_common" not in op
        assert "nullable_item" not in op

    def test_strict_via_getattr(self):
        """strict_common/strict_item work through flow.op(...) dynamic dispatch."""
        flow = Flow(
            name="getattr_strict",
            common_input=["age"],
            common_output=["result"],
        )
        flow.transform_by_lua(
            common_input=["age"],
            common_output=["result"],
            strict_common=["age"],
            lua_script="function f() return age end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        op = list(cfg["pipeline_config"]["operators"].values())[0]
        assert op["strict_common"] == ["age"]

    def test_strict_affects_unique_name(self):
        """Adding strict_common changes the auto-generated operator name."""
        from apple.base import OpCall

        op_no_strict = OpCall(
            type_name="transform_by_lua",
            params={"lua_script": "function f() return x end",
                    "function_for_item": "f", "function_for_common": ""},
            common_input=["x"],
            common_output=["y"],
        )
        op_with_strict = OpCall(
            type_name="transform_by_lua",
            params={"lua_script": "function f() return x end",
                    "function_for_item": "f", "function_for_common": ""},
            common_input=["x"],
            common_output=["y"],
            strict_common=["x"],
        )
        assert op_no_strict.unique_name() != op_with_strict.unique_name()


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
        """Lua evaluate in else/elseif must reference namespaced field via _G."""
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
        assert '_G["_my_sf::if_1"]' in ctrl_else.params["lua_script"]
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
        """Inherited skips are appended to skip and to common_input_skip (#74)."""
        from apple.base import OpCall
        from apple.compiler import _inject_inherited_skips

        op = OpCall(type_name="noop", params={}, common_input=["x"], skip=[])
        _inject_inherited_skips([op], [("op", 0)], ["_if_1"])
        assert "_if_1" in op.skip
        assert op.common_input_skip == ["_if_1"]
        assert op.common_input == ["x"]  # business bucket untouched

    def test_no_duplicate_injection(self):
        """If skip field already present, do not duplicate.

        After #74 a legacy declaration in ``common_input`` is treated as
        already covered and the engine still ranks it via the DAG union;
        we skip re-adding to ``common_input_skip``.
        """
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
        assert op.common_input_skip.count("_if_1") == 0

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


class TestUniqueNameStability:
    """#31: unique_name must be stable across different code_info values
    and sensitive to subflow_path differences."""

    def test_same_op_different_code_info_same_name(self):
        """Same operator config from different source locations must produce
        the same unique_name, since code_info is excluded from the hash."""
        from apple.base import OpCall

        op1 = OpCall(
            type_name="transform_by_lua",
            params={"lua_script": "function f() return x end",
                    "function_for_item": "f", "function_for_common": ""},
            item_input=["x"],
            item_output=["y"],
            code_info="file_a.py:10 in test(): .transform_by_lua(...)",
        )
        op2 = OpCall(
            type_name="transform_by_lua",
            params={"lua_script": "function f() return x end",
                    "function_for_item": "f", "function_for_common": ""},
            item_input=["x"],
            item_output=["y"],
            code_info="file_b.py:99 in other(): .transform_by_lua(...)",
        )
        assert op1.unique_name() == op2.unique_name()

    def test_same_op_no_code_info_same_name(self):
        """An op with empty code_info and one with non-empty code_info
        must still produce the same unique_name."""
        from apple.base import OpCall

        op1 = OpCall(
            type_name="transform_copy",
            params={"direction": "common_to_item"},
            common_input=["tag"],
            item_output=["tag"],
            code_info="",
        )
        op2 = OpCall(
            type_name="transform_copy",
            params={"direction": "common_to_item"},
            common_input=["tag"],
            item_output=["tag"],
            code_info="some_file.py:42 in build(): .transform_copy(...)",
        )
        assert op1.unique_name() == op2.unique_name()

    def test_different_subflow_path_different_name(self):
        """Same operator config placed in different SubFlow paths must
        produce different unique_names."""
        from apple.base import OpCall

        op1 = OpCall(
            type_name="transform_by_lua",
            params={"lua_script": "function f() return x end",
                    "function_for_item": "f", "function_for_common": ""},
            item_input=["x"],
            item_output=["y"],
            subflow_path="recall/candidates",
        )
        op2 = OpCall(
            type_name="transform_by_lua",
            params={"lua_script": "function f() return x end",
                    "function_for_item": "f", "function_for_common": ""},
            item_input=["x"],
            item_output=["y"],
            subflow_path="ranking/features",
        )
        assert op1.unique_name() != op2.unique_name()

    def test_explicit_name_ignores_hash(self):
        """When an explicit name is set, unique_name returns it directly."""
        from apple.base import OpCall

        op = OpCall(
            type_name="transform_copy",
            params={},
            name="my_explicit_step",
        )
        assert op.unique_name() == "my_explicit_step"


class TestSubFlowRequiredResources:
    """#37: SubFlow required_resources must be validated against parent Flow."""

    def test_missing_resource_raises(self):
        """SubFlow declares required_resources not in parent Flow → error."""
        from apple.validator import ValidationError

        sf = SubFlow(
            name="needs_feed",
            required_resources=["feed_data"],
        )
        sf._add_op(
            "transform_by_lua",
            item_input=["x"],
            item_output=["y"],
            lua_script="function f() return x end",
            function_for_item="f",
            function_for_common="",
        )

        flow = Flow(
            name="parent",
            item_input=["x"],
            item_output=["y"],
            sub_flows=[sf],
        )

        with pytest.raises(ValidationError, match="requires resource.*feed_data"):
            compile_flow(flow)

    def test_declared_resource_passes(self):
        """SubFlow declares required_resources that parent Flow has → OK."""
        from apple.resource import BaseResource

        class FakeResource(BaseResource):
            resource_type = "fake"
            interval = 60
            params: dict = {}

        sf = SubFlow(
            name="needs_feed",
            required_resources=["feed_data"],
        )
        sf._add_op(
            "transform_resource_lookup",
            item_input=["item_id"],
            item_output=["item_feed"],
            resource_name="feed_data",
            lookup_key="item_id",
            output_field="item_feed",
        )

        flow = Flow(
            name="parent",
            item_input=["item_id"],
            item_output=["item_id", "item_feed"],
            sub_flows=[sf],
        )
        flow.resource("feed_data", FakeResource())

        # Should not raise
        cfg = compile_flow(flow)
        assert "pipeline_config" in cfg


class TestThreeBucketCommonInput:
    """#74: $metadata gains common_input_skip / common_input_template buckets.

    Verifies the compiler routes template source fields into
    common_input_template (not common_input) and emits the optional
    buckets only when populated.
    """

    def test_template_fields_go_to_template_bucket(self):
        """Template `{{...}}` references land in common_input_template."""
        flow = Flow(
            name="t",
            common_input=["user_id"],
            common_output=["greeting"],
        )
        # Synthetic op type (no apple_generated schema) — Pass 0 still
        # walks params via extract_fields_from_params; the templated-
        # param schema validator no-ops when the schema is unknown,
        # letting us isolate the bucket-routing behaviour.
        flow._add_op(
            "synthetic_templated_op",
            common_input=[],
            common_output=["greeting"],
            key_template="hi {{user_id}}",
        )
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        assert "user_id" in op["$metadata"].get("common_input_template", [])
        assert "user_id" not in op["$metadata"]["common_input"]

    def test_optional_buckets_omitted_when_empty(self):
        """Operators without skip/template deps emit no extra metadata keys."""
        flow = Flow(name="plain", common_input=["a"], common_output=["b"])
        flow._add_op(
            "transform_by_lua",
            common_input=["a"],
            common_output=["b"],
            lua_script="function f() return a end",
            function_for_common="f",
            function_for_item="",
        )
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        meta = op["$metadata"]
        assert "common_input_skip" not in meta
        assert "common_input_template" not in meta


class TestSubFlowContractEnforcement:
    """#78: SubFlow common_input/item_input/output contracts are enforced.

    Companion to TestSubFlowRequiredResources (#37) which covered the
    resource-only path. These tests cover the four field-list contracts.
    """

    def _add_lua(self, node, *, common_input=None, common_output=None,
                 item_input=None, item_output=None, name=None):
        # Helper: minimal transform_by_lua with no real Lua semantics —
        # we only care about input/output bookkeeping for compile-time
        # contract checks.
        kwargs = {
            "common_input": common_input or [],
            "common_output": common_output or [],
            "item_input": item_input or [],
            "item_output": item_output or [],
            "lua_script": "function f() return 0 end",
            "function_for_item": "f",
            "function_for_common": "",
        }
        if name:
            kwargs["name"] = name
        node._add_op("transform_by_lua", **kwargs)

    def test_input_contract_missing_field_raises(self):
        """SubFlow declares item_input but reads a field outside it → error."""
        from apple.validator import ValidationError

        sf = SubFlow(name="sf", item_input=["x"])
        # Reads y, which is NOT in the SubFlow contract even though parent
        # provides it; the SubFlow-scoped check fires first.
        self._add_lua(sf, item_input=["y"], item_output=["z"])

        flow = Flow(
            name="parent",
            item_input=["x", "y"],
            item_output=["z"],
            sub_flows=[sf],
        )
        with pytest.raises(ValidationError, match=r"SubFlow 'sf' contract"):
            compile_flow(flow)

    def test_input_contract_satisfied_passes(self):
        """SubFlow contract covers all reads → compiles."""
        sf = SubFlow(name="sf", item_input=["x"], item_output=["z"])
        self._add_lua(sf, item_input=["x"], item_output=["z"])

        flow = Flow(
            name="parent",
            item_input=["x"],
            item_output=["z"],
            sub_flows=[sf],
        )
        cfg = compile_flow(flow)
        assert "pipeline_config" in cfg

    def test_input_contract_internal_upstream_satisfies(self):
        """Internal upstream op output satisfies a downstream read inside the SubFlow."""
        sf = SubFlow(name="sf", item_input=["x"], item_output=["z"])
        self._add_lua(sf, item_input=["x"], item_output=["mid"])
        self._add_lua(sf, item_input=["mid"], item_output=["z"])

        flow = Flow(
            name="parent",
            item_input=["x"],
            item_output=["z"],
            sub_flows=[sf],
        )
        cfg = compile_flow(flow)
        assert "pipeline_config" in cfg

    def test_nested_subflow_inherits_outer_contract(self):
        """Nested SubFlow without its own contract is checked against the
        outer SubFlow's contract — chosen semantics for #78."""
        from apple.validator import ValidationError

        inner = SubFlow(name="inner")
        # inner reads `bad`, which the outer SubFlow does NOT advertise.
        self._add_lua(inner, item_input=["bad"], item_output=["z"])

        outer = SubFlow(name="outer", item_input=["x"], item_output=["z"])
        outer.add_subflow(inner)

        flow = Flow(
            name="parent",
            item_input=["x", "bad"],
            item_output=["z"],
            sub_flows=[outer],
        )
        with pytest.raises(ValidationError, match=r"SubFlow 'outer' contract"):
            compile_flow(flow)

    def test_output_contract_dead_internal_op_raises(self):
        """SubFlow declares item_output; an internal op whose ENTIRE output
        is consumed only OUTSIDE the SubFlow is dead by SubFlow scope.

        Mirrors the flow-level detect_dead_code's op-granularity rule:
        an op is dead iff none of its outputs are needed within scope.
        """
        from apple.validator import ValidationError

        sf = SubFlow(name="sf", item_input=["x"], item_output=["z"])
        # First op produces `z` to satisfy the SubFlow output contract.
        self._add_lua(sf, item_input=["x"], item_output=["z"])
        # Second op produces `extra` only — consumed by the parent Flow's
        # item_output but not by anything inside the SubFlow, and `extra`
        # is not in the SubFlow's item_output contract → dead by SubFlow.
        self._add_lua(sf, item_input=["x"], item_output=["extra"], name="dead_op")

        flow = Flow(
            name="parent",
            item_input=["x"],
            item_output=["z", "extra"],
            sub_flows=[sf],
        )
        with pytest.raises(ValidationError, match=r"SubFlow 'sf'.*dead"):
            compile_flow(flow)

    def test_output_contract_consumed_internally_passes(self):
        """A field is consumed by a downstream op INSIDE the SubFlow
        even though it's not in the SubFlow's output contract."""
        sf = SubFlow(name="sf", item_input=["x"], item_output=["z"])
        self._add_lua(sf, item_input=["x"], item_output=["mid"])
        self._add_lua(sf, item_input=["mid"], item_output=["z"])

        flow = Flow(
            name="parent",
            item_input=["x"],
            item_output=["z"],
            sub_flows=[sf],
        )
        cfg = compile_flow(flow)
        assert "pipeline_config" in cfg

    def test_no_contract_no_check_backwards_compat(self):
        """SubFlow without any field-list contract must compile unchanged
        (back-compat with existing SubFlow callsites)."""
        sf = SubFlow(name="sf")
        # Reads `external_x` — only the parent flow contract covers it.
        # Without a SubFlow input contract, only parent-level coverage runs.
        self._add_lua(sf, item_input=["external_x"], item_output=["z"])

        flow = Flow(
            name="parent",
            item_input=["external_x"],
            item_output=["z"],
            sub_flows=[sf],
        )
        cfg = compile_flow(flow)
        assert "pipeline_config" in cfg

    def test_skip_dead_code_flag_skips_subflow_check_too(self):
        """Flow's skip_dead_code=True also bypasses SubFlow dead-code."""
        sf = SubFlow(name="sf", item_input=["x"], item_output=["z"])
        self._add_lua(sf, item_input=["x"], item_output=["z"])
        # Same dead-by-SubFlow shape as test_output_contract_dead_internal_op_raises;
        # this op is dead-by-SubFlow but skip_dead_code lets it through.
        self._add_lua(sf, item_input=["x"], item_output=["extra"], name="dead_op")

        flow = Flow(
            name="parent",
            item_input=["x"],
            item_output=["z", "extra"],
            sub_flows=[sf],
            skip_dead_code=True,
        )
        cfg = compile_flow(flow)
        assert "pipeline_config" in cfg

    def test_mixed_common_and_item_contract(self):
        """SubFlow declares both common_input and item_input simultaneously;
        each side gates its own check independently and both must be
        satisfied."""
        from apple.validator import ValidationError

        # Happy path: both sides satisfied.
        sf_ok = SubFlow(
            name="sf",
            common_input=["uid"],
            item_input=["x"],
            common_output=["greeting"],
            item_output=["z"],
        )
        self._add_lua(
            sf_ok,
            common_input=["uid"],
            common_output=["greeting"],
            item_input=["x"],
            item_output=["z"],
        )
        flow_ok = Flow(
            name="parent",
            common_input=["uid"],
            item_input=["x"],
            common_output=["greeting"],
            item_output=["z"],
            sub_flows=[sf_ok],
        )
        assert "pipeline_config" in compile_flow(flow_ok)

        # Item side missing: common contract satisfied but item op reads
        # `missing_item` not in item_input contract → raise.
        sf_bad_item = SubFlow(
            name="sf",
            common_input=["uid"],
            item_input=["x"],
            common_output=["greeting"],
            item_output=["z"],
        )
        self._add_lua(
            sf_bad_item,
            common_input=["uid"],
            common_output=["greeting"],
            item_input=["missing_item"],
            item_output=["z"],
        )
        flow_bad_item = Flow(
            name="parent",
            common_input=["uid"],
            item_input=["x", "missing_item"],
            common_output=["greeting"],
            item_output=["z"],
            sub_flows=[sf_bad_item],
        )
        with pytest.raises(ValidationError, match=r"item_input field 'missing_item'"):
            compile_flow(flow_bad_item)

        # Common side missing: item contract satisfied but common op
        # reads `missing_common` not in common_input contract → raise.
        sf_bad_common = SubFlow(
            name="sf",
            common_input=["uid"],
            item_input=["x"],
            common_output=["greeting"],
            item_output=["z"],
        )
        self._add_lua(
            sf_bad_common,
            common_input=["missing_common"],
            common_output=["greeting"],
            item_input=["x"],
            item_output=["z"],
        )
        flow_bad_common = Flow(
            name="parent",
            common_input=["uid", "missing_common"],
            item_input=["x"],
            common_output=["greeting"],
            item_output=["z"],
            sub_flows=[sf_bad_common],
        )
        with pytest.raises(ValidationError, match=r"common_input field 'missing_common'"):
            compile_flow(flow_bad_common)


class TestMarkerWidenSemantics:
    """Row-set marker merge between the codegen registry and caller overrides
    on `_FlowBase._add_op` (see flow.py:138-145).

    The contract is True-OR: the effective marker is `registry OR caller`. This
    means a caller can only widen a marker (False→True), never narrow one
    (True→False). The registry stays the source of truth — it mirrors the Go
    interface assertions (ConsumesRowSet / AdditiveWritesRowSet /
    MutatesRowSet), and DSL callsites must not be able to lie about an
    operator's row-set semantics. `_add_op` accepts overrides on all three
    markers because it is the back-channel dynamic-dispatch path used when an
    operator has not yet declared the matching marker interface on the Go
    side. `BaseOp._apply` only exposes consumes_row_set as a widen-knob (see
    base.py:151-161) — additive/mutates are taken purely from the registry.

    Audit gaps M3 + M4.
    """

    def _add_lua_with_overrides(self, flow, **overrides):
        flow._add_op(
            "transform_by_lua",
            item_input=["x"], item_output=["y"],
            lua_script="function f() return 0 end",
            function_for_item="f", function_for_common="",
            **overrides,
        )

    def test_registry_false_caller_widens_all_three_markers(self):
        """transform_by_lua is all-False in the registry. _add_op accepts
        override=True for all three markers and the compiled JSON reflects
        the widened values."""
        flow = Flow(name="t", item_input=["x"], item_output=["y"])
        self._add_lua_with_overrides(
            flow,
            consumes_row_set=True,
            additive_writes_row_set=True,
            mutates_row_set=True,
        )
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        assert op.get("consumes_row_set") is True
        assert op.get("additive_writes_row_set") is True
        assert op.get("mutates_row_set") is True

    def test_registry_false_no_override_keeps_all_false(self):
        """Without overrides, transform_by_lua's compiled JSON omits the
        markers (False is the default and is not emitted)."""
        flow = Flow(name="t", item_input=["x"], item_output=["y"])
        self._add_lua_with_overrides(flow)
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        assert "consumes_row_set" not in op
        assert "additive_writes_row_set" not in op
        assert "mutates_row_set" not in op

    def test_registry_true_caller_false_does_not_narrow(self):
        """filter_truncate has consumes=True, mutates=True in the registry.
        A caller passing override=False MUST NOT downgrade the effective
        markers — the registry wins under True-OR. This is the protection
        flow.py:138-145 documents: DSL callsites cannot narrow a marker the
        Go interface assertion has set."""
        flow = Flow(name="t", common_input=["top_n"], item_input=["x"], item_output=["x"])
        flow._add_op(
            "filter_truncate",
            common_input=["top_n"], item_input=["x"], item_output=["x"],
            top_n=10,
            consumes_row_set=False,
            mutates_row_set=False,
        )
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        assert op.get("consumes_row_set") is True
        assert op.get("mutates_row_set") is True

    def test_registry_true_caller_true_idempotent(self):
        """True OR True is True — caller restating the registry value must
        not flip the marker or duplicate-emit anything."""
        flow = Flow(name="t", common_input=["top_n"], item_input=["x"], item_output=["x"])
        flow._add_op(
            "filter_truncate",
            common_input=["top_n"], item_input=["x"], item_output=["x"],
            top_n=10,
            consumes_row_set=True,
            mutates_row_set=True,
        )
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        assert op.get("consumes_row_set") is True
        assert op.get("mutates_row_set") is True

    def test_caller_partial_override_independent_per_marker(self):
        """The three markers are merged independently. Widening one must
        not affect the others."""
        flow = Flow(name="t", item_input=["x"], item_output=["y"])
        self._add_lua_with_overrides(flow, additive_writes_row_set=True)
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        assert "consumes_row_set" not in op
        assert op.get("additive_writes_row_set") is True
        assert "mutates_row_set" not in op

    def test_truthy_override_coerced_to_bool(self):
        """flow.py:127 `bool(v)` — non-bool truthy values widen the marker
        through the same True-OR path. This is the documented coercion
        contract; without it a caller passing 1 / "yes" / a non-empty list
        could silently no-op against a registry-False marker."""
        flow = Flow(name="t", item_input=["x"], item_output=["y"])
        self._add_lua_with_overrides(flow, consumes_row_set=1)
        cfg = compile_flow(flow)
        op = next(iter(cfg["pipeline_config"]["operators"].values()))
        assert op.get("consumes_row_set") is True

