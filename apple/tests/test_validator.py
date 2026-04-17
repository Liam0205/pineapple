"""Tests for DSL-side validation."""
import pytest
import sys
import os

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple.flow import Flow
from apple.validator import ValidationError


class TestFieldCoverage:
    def test_missing_common_input(self):
        flow = Flow(name="bad", common_input=[])
        flow._add_op("lua", common_input=["missing_field"],
                      common_output=["out"],
                      lua_script="function f() return 1 end",
                      function_for_common="f", function_for_item="")
        with pytest.raises(ValidationError, match="missing_field"):
            flow.compile()

    def test_missing_item_input(self):
        flow = Flow(name="bad", item_input=[])
        flow._add_op("lua", item_input=["missing_item"],
                      item_output=["out"],
                      lua_script="function f() return 1 end",
                      function_for_item="f", function_for_common="")
        with pytest.raises(ValidationError, match="missing_item"):
            flow.compile()

    def test_upstream_output_satisfies_input(self):
        flow = Flow(name="chain", common_input=["x"])
        flow._add_op("lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow._add_op("lua", common_input=["y"], common_output=["z"],
                      lua_script="function g() return y end",
                      function_for_common="g", function_for_item="")
        # Should not raise — y is produced by first op
        flow.compile()


class TestWriteWithoutRead:
    def test_overwrite_without_read(self):
        flow = Flow(name="bad", common_input=["x"])
        flow._add_op("lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        # Writing y again without reading it — y was written by upstream op
        flow._add_op("lua", common_output=["y"],
                      lua_script="function g() return 42 end",
                      function_for_common="g", function_for_item="")
        with pytest.raises(ValidationError, match="writes common field"):
            flow.compile()

    def test_read_then_write_ok(self):
        flow = Flow(name="ok", common_input=["x"], common_output=["y"])
        flow._add_op("lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        # Reading y and writing y is OK
        flow._add_op("lua", common_input=["y"], common_output=["y"],
                      lua_script="function g() return y * 2 end",
                      function_for_common="g", function_for_item="")
        flow.compile()  # should not raise


class TestDeadCode:
    def test_dead_operator(self):
        flow = Flow(
            name="dead",
            common_input=["x"],
            common_output=["y"],
            item_output=[],
        )
        flow._add_op("lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        # This op produces z, which nobody consumes
        flow._add_op("lua", common_input=["x"], common_output=["z"],
                      lua_script="function g() return x * 2 end",
                      function_for_common="g", function_for_item="")
        with pytest.raises(ValidationError, match="dead operators"):
            flow.compile()

    def test_no_dead_code_when_output_not_declared(self):
        flow = Flow(name="ok", common_input=["x"])
        flow._add_op("lua", common_input=["x"], common_output=["y"],
                      lua_script="function f() return x end",
                      function_for_common="f", function_for_item="")
        flow._add_op("lua", common_input=["x"], common_output=["z"],
                      lua_script="function g() return x end",
                      function_for_common="g", function_for_item="")
        # No output contract declared — no dead code detection
        flow.compile()  # should not raise


class TestControlFlowValidation:
    def test_elseif_without_if(self):
        flow = Flow(name="bad")
        with pytest.raises(ValueError, match="elseif_ without matching if_"):
            flow.elseif_("x > 0")

    def test_else_without_if(self):
        flow = Flow(name="bad")
        with pytest.raises(ValueError, match="else_ without matching if_"):
            flow.else_()

    def test_end_if_without_if(self):
        flow = Flow(name="bad")
        with pytest.raises(ValueError, match="end_if_ without matching if_"):
            flow.end_if_()
