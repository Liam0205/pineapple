"""Tests for DSL-side validation."""
import os
import sys

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple.flow import Flow
from apple.validator import ValidationError


class TestFieldCoverage:
    def test_missing_common_input(self):
        flow = Flow(name="bad", common_input=[])
        flow._add_op("transform_by_lua", common_input=["missing_field"],
                      common_output=["out"],
                      lua_script="function f() return 1 end",
                      function_for_common="f", function_for_item="")
        with pytest.raises(ValidationError, match="missing_field"):
            flow.compile()

    def test_missing_item_input(self):
        flow = Flow(name="bad", item_input=[])
        flow._add_op("transform_by_lua", item_input=["missing_item"],
                      item_output=["out"],
                      lua_script="function f() return 1 end",
                      function_for_item="f", function_for_common="")
        with pytest.raises(ValidationError, match="missing_item"):
            flow.compile()

    def test_upstream_output_satisfies_input(self):
        flow = Flow(name="chain", common_input=["x"], common_output=["z"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow._add_op("transform_by_lua", common_input=["y"], common_output=["z"],
                      lua_script="function g() return y end",
                      function_for_common="g", function_for_item="")
        # Should not raise — y is produced by first op, z consumed by flow output
        flow.compile()


class TestWriteWithoutRead:
    def test_overwrite_without_read(self):
        flow = Flow(name="bad", common_input=["x"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        # Writing y again without reading it — y was written by upstream op
        flow._add_op("transform_by_lua", common_output=["y"],
                      lua_script="function g() return 42 end",
                      function_for_common="g", function_for_item="")
        with pytest.raises(ValidationError, match="writes common field"):
            flow.compile()

    def test_read_then_write_ok(self):
        flow = Flow(name="ok", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        # Reading y and writing y is OK
        flow._add_op("transform_by_lua", common_input=["y"], common_output=["y"],
                      lua_script="function g() return y * 2 end",
                      function_for_common="g", function_for_item="")
        flow.compile()  # should not raise

    def test_if_else_branches_write_same_field_ok(self):
        """Mutually exclusive if/else branches may write the same field."""
        flow = Flow(name="ok", common_input=["x"], common_output=["salt"])
        flow.if_("{{x}} ~= nil") \
            ._add_op("transform_by_lua",
                     common_input=["x"], common_output=["salt"],
                     lua_script="function f() return x end",
                     function_for_common="f", function_for_item="") \
        .else_() \
            ._add_op("transform_by_lua",
                     common_output=["salt"],
                     lua_script="function g() return 'default' end",
                     function_for_common="g", function_for_item="") \
        .end_if_()
        flow.compile()  # should not raise


class TestDeadCode:
    def test_dead_operator(self):
        flow = Flow(
            name="dead",
            common_input=["x"],
            common_output=["y"],
            item_output=[],
        )
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        # This op produces z, which nobody consumes
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["z"],
                      lua_script="function g() return x * 2 end",
                      function_for_common="g", function_for_item="")
        with pytest.raises(ValidationError, match="dead operators"):
            flow.compile()

    def test_dead_code_detected_even_without_output_contract(self):
        """Without declared output contract, ops whose output is not consumed
        downstream are still flagged as dead."""
        flow = Flow(name="dead_no_contract", common_input=["x"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["z"],
                      lua_script="function g() return x end",
                      function_for_common="g", function_for_item="")
        # y and z are not consumed by any downstream op — dead
        with pytest.raises(ValidationError, match="dead operators"):
            flow.compile()


class TestObserveExemption:
    def test_observe_op_not_dead_code(self):
        """Observe operators (no output fields) should be exempt from dead-code detection."""
        flow = Flow(
            name="observe_ok",
            common_input=["x"],
            common_output=["y"],
            item_output=[],
        )
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        # Observe-style op: reads x but produces no output
        flow._add_op("transform_by_lua", common_input=["x"],
                      common_output=[], item_output=[],
                      lua_script="function obs() end",
                      function_for_common="obs", function_for_item="")
        # Should NOT raise — observe ops are exempt from dead-code detection
        flow.compile()

    def test_op_with_output_still_detected_as_dead(self):
        """Ops that DO produce output but nobody consumes it should still be flagged."""
        flow = Flow(
            name="dead_with_output",
            common_input=["x"],
            common_output=["y"],
            item_output=[],
        )
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["z"],
                      lua_script="function g() return x end",
                      function_for_common="g", function_for_item="")
        with pytest.raises(ValidationError, match="dead operators"):
            flow.compile()


class TestControlFlowValidation:
    def test_elseif_without_if(self):
        flow = Flow(name="bad")
        with pytest.raises(ValueError, match="elseif_ without matching if_"):
            flow.elseif_("{{x}} > 0")

    def test_else_without_if(self):
        flow = Flow(name="bad")
        with pytest.raises(ValueError, match="else_ without matching if_"):
            flow.else_()

    def test_end_if_without_if(self):
        flow = Flow(name="bad")
        with pytest.raises(ValueError, match="end_if_ without matching if_"):
            flow.end_if_()

    def test_unclosed_if_raises(self):
        flow = Flow(name="bad", common_input=["x"], common_output=["y"])
        flow.if_("{{x}} ~= nil")
        flow._add_op("transform_by_lua",
                      common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        with pytest.raises(ValidationError, match="unclosed if_ block"):
            flow.compile()

    def test_unclosed_if_in_subflow_raises(self):
        from apple.flow import SubFlow
        sf = SubFlow(name="bad_sub")
        sf.if_("true")
        sf._add_op("transform_by_lua",
                    common_output=["y"],
                    lua_script="function f() return 1 end",
                    function_for_common="f", function_for_item="")

        flow = Flow(name="main", common_input=["x"], common_output=["y"],
                     sub_flows=[sf])
        with pytest.raises(ValidationError, match="unclosed if_ block in 'bad_sub'"):
            flow.compile()

    def test_empty_if_branch_raises(self):
        flow = Flow(name="bad", common_input=["x"], common_output=["y"])
        with pytest.raises(ValueError, match="empty if branch"):
            flow.if_("{{x}} ~= nil").end_if_()

    def test_empty_else_branch_raises(self):
        flow = Flow(name="bad", common_input=["x"], common_output=["y"])
        flow.if_("{{x}} ~= nil")
        flow._add_op("transform_by_lua",
                      common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow.else_()
        with pytest.raises(ValueError, match="empty else branch"):
            flow.end_if_()


class TestUnderscorePrefix:
    def test_underscore_in_flow_common_output_rejected(self):
        flow = Flow(name="bad", common_input=["x"], common_output=["_x"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["_x"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        with pytest.raises(ValidationError, match="flow common_output.*_x.*reserved"):
            flow.compile()

    def test_underscore_in_flow_item_output_rejected(self):
        flow = Flow(name="bad", common_input=["x"], item_output=["_y"])
        flow._add_op("transform_by_lua", common_input=["x"], item_output=["_y"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="")
        with pytest.raises(ValidationError, match="flow item_output.*_y.*reserved"):
            flow.compile()

    def test_underscore_in_op_common_output_rejected(self):
        flow = Flow(name="bad", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"], common_output=["_bad"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        with pytest.raises(ValidationError, match="common_output.*_bad.*reserved"):
            flow.compile()

    def test_underscore_in_op_item_output_rejected(self):
        flow = Flow(name="bad", common_input=["x"], item_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"], item_output=["_bad"],
                      lua_script="function f() return x end",
                      function_for_item="f", function_for_common="")
        with pytest.raises(ValidationError, match="item_output.*_bad.*reserved"):
            flow.compile()

    def test_underscore_in_input_allowed(self):
        """Users may read engine-internal fields like _source via inputs."""
        flow = Flow(name="ok", common_input=["_source", "x"], common_output=["y"])
        flow._add_op("transform_by_lua",
                      common_input=["_source", "x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow.compile()  # should not raise

    def test_control_op_underscore_allowed(self):
        """if_/else_ control ops produce _if_* fields — these must be allowed."""
        flow = Flow(name="ok", common_input=["x"], common_output=["y"])
        flow.if_("{{x}} ~= nil") \
            ._add_op("transform_by_lua",
                     common_input=["x"], common_output=["y"],
                     lua_script="function f() return x end",
                     function_for_common="f", function_for_item="") \
            .end_if_()
        flow.compile()  # should not raise


class TestDataParallel:
    def test_transform_data_parallel_ok(self):
        """Transform with no common_output and data_parallel > 1 should pass."""
        flow = Flow(name="ok", common_input=["x"], item_input=["a"], item_output=["b"])
        flow._add_op("transform_by_lua", common_input=["x"],
                      item_input=["a"], item_output=["b"],
                      lua_script="function f() return a end",
                      function_for_item="f", function_for_common="",
                      data_parallel=4)
        flow.compile()  # should not raise

    def test_non_transform_data_parallel_rejected(self):
        """Non-transform operator with data_parallel > 1 must be rejected."""
        flow = Flow(name="bad", item_output=["item_id"])
        flow._add_op("recall_static", item_output=["item_id"],
                      items=[{"item_id": "a"}], data_parallel=2)
        with pytest.raises(ValidationError, match="only supported for Transform"):
            flow.compile()

    def test_data_parallel_with_common_output_rejected(self):
        """Transform with common_output and data_parallel > 1 must be rejected."""
        flow = Flow(name="bad", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"],
                      common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="",
                      data_parallel=2)
        with pytest.raises(ValidationError, match="requires empty common_output"):
            flow.compile()

    def test_data_parallel_whole_item_set_transform_rejected(self):
        """Whole-item-set transforms must not be silently sharded."""
        flow = Flow(name="bad", item_input=["score"], item_output=["norm"])
        flow._add_op("transform_normalize", item_input=["score"],
                      item_output=["norm"], data_parallel=2)
        with pytest.raises(ValidationError, match="whole-item-set semantics"):
            flow.compile()

    def test_data_parallel_one_ok(self):
        """data_parallel=1 should not trigger validation."""
        flow = Flow(name="ok", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"],
                      common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="",
                      data_parallel=1)
        flow.compile()  # should not raise

    def test_data_parallel_zero_ok(self):
        """data_parallel=0 (default) should not trigger validation."""
        flow = Flow(name="ok", common_input=["x"], common_output=["y"])
        flow._add_op("transform_by_lua", common_input=["x"],
                      common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="",
                      data_parallel=0)
        flow.compile()  # should not raise


class TestParamMetadataConsistency:
    def _make_resource(self, name: str = "res"):
        from apple.resource import BaseResource
        r = BaseResource(interval=600)
        r.resource_type = name
        return r

    def test_resource_lookup_missing_lookup_key_in_item_input(self):
        flow = Flow(name="bad", item_input=["item_id"], item_output=["feat"])
        flow.resource("res", self._make_resource())
        flow._add_op("transform_resource_lookup",
                      item_input=[],
                      item_output=["feat"],
                      resource_name="res", lookup_key="item_id",
                      output_field="feat")
        with pytest.raises(ValidationError, match="lookup_key.*item_input"):
            flow.compile()

    def test_resource_lookup_missing_output_field_in_item_output(self):
        flow = Flow(name="bad", item_input=["item_id"], item_output=["feat"])
        flow.resource("res", self._make_resource())
        flow._add_op("transform_resource_lookup",
                      item_input=["item_id"],
                      item_output=[],
                      resource_name="res", lookup_key="item_id",
                      output_field="feat")
        with pytest.raises(ValidationError, match="output_field.*item_output"):
            flow.compile()

    def test_resource_lookup_correct_metadata_ok(self):
        flow = Flow(name="ok", item_input=["item_id"], item_output=["feat"])
        flow.resource("res", self._make_resource())
        flow._add_op("transform_resource_lookup",
                      item_input=["item_id"],
                      item_output=["feat"],
                      resource_name="res", lookup_key="item_id",
                      output_field="feat")
        flow.compile()  # should not raise
