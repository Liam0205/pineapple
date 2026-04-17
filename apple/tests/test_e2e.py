"""End-to-end test: Apple DSL → JSON config → Pine engine execution.

This test:
1. Declares a pipeline using the Apple DSL
2. Compiles it to JSON
3. Writes the JSON to a temp file
4. Runs the Pine engine (via Go test) with that JSON
5. Validates the results
"""
import json
import os
import subprocess
import sys
import tempfile

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple.flow import Flow, SubFlow


class TestE2E:
    def _compile_and_write(self, flow: Flow) -> str:
        """Compile flow to JSON and write to a temp file. Returns path."""
        json_str = flow.compile()
        # Validate JSON is parseable
        parsed = json.loads(json_str)
        assert "pipeline_config" in parsed

        fd, path = tempfile.mkstemp(suffix=".json", prefix="apple_e2e_")
        os.write(fd, json_str.encode())
        os.close(fd)
        return path

    def test_simple_pipeline_json_structure(self):
        """Compile a simple pipeline and verify JSON matches Pine expectations."""
        flow = Flow(
            name="e2e_simple",
            common_input=["user_age"],
            item_output=["item_id", "item_final_price"],
        )
        # Recall items
        flow._add_op(
            "recall_static",
            item_output=["item_id", "item_price"],
            items=[
                {"item_id": "a", "item_price": 100.0},
                {"item_id": "b", "item_price": 200.0},
            ],
        )
        # Lua discount
        flow._add_op(
            "lua",
            common_input=["user_age"],
            item_input=["item_price"],
            item_output=["item_final_price"],
            lua_script=(
                "function apply_discount()\n"
                "  if user_age < 18 then\n"
                "    return item_price * 0.8\n"
                "  else\n"
                "    return item_price\n"
                "  end\n"
                "end"
            ),
            function_for_item="apply_discount",
            function_for_common="",
        )

        path = self._compile_and_write(flow)
        try:
            with open(path) as f:
                cfg = json.load(f)

            # Verify structure
            assert cfg["_PINEAPPLE_VERSION"] == "0.1.0"
            ops = cfg["pipeline_config"]["operators"]
            assert len(ops) == 2

            # Verify recall operator
            recall_ops = [o for o in ops.values() if o["type_name"] == "recall_static"]
            assert len(recall_ops) == 1

            # Verify lua operator
            lua_ops = [o for o in ops.values() if o["type_name"] == "lua"]
            assert len(lua_ops) == 1
            assert "function apply_discount()" in lua_ops[0]["lua_script"]

            # Verify flow contract
            assert cfg["flow_contract"]["common_input"] == ["user_age"]

            # Verify pipeline structure
            pmap = cfg["pipeline_config"]["pipeline_map"]
            assert len(pmap) == 1
            group = cfg["pipeline_group"]["main"]["pipeline"]
            assert len(group) == 1
        finally:
            os.unlink(path)

    def test_control_flow_pipeline_json(self):
        """Compile a pipeline with control flow and verify the lowered JSON."""
        flow = Flow(
            name="e2e_control",
            common_input=["user_age"],
            item_input=["item_score"],
            item_output=["item_score"],
        )
        flow.if_("user_age > 18") \
            .reorder_sort(
                item_input=["item_score"],
                field="item_score",
                order="desc",
            ) \
        .end_if_()

        path = self._compile_and_write(flow)
        try:
            with open(path) as f:
                cfg = json.load(f)

            ops = cfg["pipeline_config"]["operators"]
            assert len(ops) == 2

            # Control op
            ctrl = [o for o in ops.values() if o.get("for_branch_control")]
            assert len(ctrl) == 1
            assert ctrl[0]["$metadata"]["common_output"] == ["_if_1"]

            # Sort op has skip
            sort = [o for o in ops.values() if o["type_name"] == "reorder_sort"]
            assert len(sort) == 1
            assert sort[0]["skip"] == "_if_1"
        finally:
            os.unlink(path)

    def test_subflow_composition_json(self):
        """Compile a pipeline with sub-flows."""
        recall_stage = SubFlow(name="recall")
        recall_stage._add_op(
            "recall_static",
            item_output=["item_id", "item_score"],
            items=[{"item_id": "a", "item_score": 1.0}],
            recall=True,
        )

        rank_stage = SubFlow(name="ranking")
        rank_stage.reorder_sort(
            item_input=["item_score"],
            field="item_score",
            order="desc",
        )

        flow = Flow(
            name="e2e_subflows",
            item_output=["item_id", "item_score"],
            sub_flows=[recall_stage, rank_stage],
        )
        path = self._compile_and_write(flow)
        try:
            with open(path) as f:
                cfg = json.load(f)

            pmap = cfg["pipeline_config"]["pipeline_map"]
            assert "recall" in pmap
            assert "ranking" in pmap
            group = cfg["pipeline_group"]["main"]["pipeline"]
            assert group == ["recall", "ranking"]
        finally:
            os.unlink(path)

    def test_go_engine_executes_dsl_json(self):
        """Full end-to-end: DSL → JSON → Go engine execution.

        Writes a compiled config to testdata/ and runs the Go integration test.
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
            "lua",
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
        flow.reorder_sort(
            item_input=["item_final_price"],
            field="item_final_price",
            order="desc",
        )

        # Write to testdata
        json_str = flow.compile()
        config_path = os.path.join(
            os.path.dirname(__file__), "..", "..", "testdata", "e2e_apple_dsl.json"
        )
        with open(config_path, "w") as f:
            f.write(json_str)

        # Run the Go test
        result = subprocess.run(
            ["go", "test", "./integration/", "-run", "TestAppleDSLe2e", "-v", "-count=1"],
            capture_output=True,
            text=True,
            cwd=os.path.join(os.path.dirname(__file__), "..", ".."),
            timeout=60,
        )
        print(result.stdout)
        if result.returncode != 0:
            print(result.stderr)
        assert result.returncode == 0, f"Go test failed:\n{result.stdout}\n{result.stderr}"
