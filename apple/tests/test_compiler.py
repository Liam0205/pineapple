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
                field="item_score", order="desc",
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
        assert sort_op["skip"] == "_if_1"

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
                field="item_score", order="desc",
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
        skip_fields = [o.get("skip") for o in biz_ops]
        assert "_if_1" in skip_fields
        assert any("_elif_" in (s or "") for s in skip_fields)
        assert any("_else_" in (s or "") for s in skip_fields)

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
