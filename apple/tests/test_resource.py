"""Tests for resource declaration DSL and unified config compilation."""
import json
import pytest
import sys
import os

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple.flow import Flow
from apple.compiler import compile_flow
from apple.resource import BaseResource
from apple.validator import ValidationError


class MockDbResource(BaseResource):
    """A test resource type for unit tests."""
    _name = "test_db"
    _default_interval = 300
    _params_schema = {"dsn": {"type": "string", "required": True}}

    def __init__(self, *, dsn: str, interval: int = 300):
        super().__init__(interval=interval, dsn=dsn)


class TestResourceCompile:
    def test_resource_in_compiled_output(self):
        flow = Flow(
            name="res_test",
            common_input=["user_id"],
            item_output=["item_id"],
        )
        flow.resource("my_db", MockDbResource(dsn="host:3306/db"))
        flow.recall_static(
            resource_name="my_db",
            item_output=["item_id"],
            items=[],
        )

        cfg = compile_flow(flow)
        assert "resource_config" in cfg
        rc = cfg["resource_config"]
        assert "my_db" in rc
        assert rc["my_db"]["type"] == "test_db"
        assert rc["my_db"]["interval"] == 300
        assert rc["my_db"]["params"]["dsn"] == "host:3306/db"

    def test_resource_custom_interval(self):
        flow = Flow(
            name="res_test",
            common_input=["user_id"],
            item_output=["item_id"],
        )
        flow.resource("my_db", MockDbResource(dsn="host:3306/db", interval=60))
        flow.recall_static(
            resource_name="my_db",
            item_output=["item_id"],
            items=[],
        )

        cfg = compile_flow(flow)
        assert cfg["resource_config"]["my_db"]["interval"] == 60

    def test_no_resources_no_key(self):
        flow = Flow(
            name="no_res",
            common_input=["x"],
            item_output=["y"],
        )
        flow.recall_static(item_output=["y"], items=[])

        cfg = compile_flow(flow)
        assert "resource_config" not in cfg

    def test_resource_type_from_class(self):
        """resource_type comes from BaseResource._name."""
        res = MockDbResource(dsn="foo")
        assert res.resource_type == "test_db"
        assert res.params == {"dsn": "foo"}

    def test_resource_default_interval(self):
        res = MockDbResource(dsn="foo")
        assert res.interval == 300  # from _default_interval

    def test_resource_json_roundtrip(self):
        flow = Flow(
            name="roundtrip",
            common_input=["x"],
            item_output=["y"],
        )
        flow.resource("data", MockDbResource(dsn="mysql://localhost"))
        flow.recall_static(
            resource_name="data",
            item_output=["y"],
            items=[],
        )

        json_str = flow.compile()
        parsed = json.loads(json_str)
        assert parsed["resource_config"]["data"]["type"] == "test_db"
        assert parsed["resource_config"]["data"]["params"]["dsn"] == "mysql://localhost"


class TestResourceValidation:
    def test_missing_resource_declaration(self):
        """Operator references resource_name but no matching resource declared."""
        flow = Flow(
            name="missing_res",
            common_input=["x"],
            item_output=["y"],
        )
        # No flow.resource() call
        flow.recall_static(
            resource_name="nonexistent",
            item_output=["y"],
            items=[],
        )

        with pytest.raises(ValidationError, match="nonexistent"):
            compile_flow(flow)

    def test_valid_resource_reference(self):
        """Operator references a declared resource — should pass."""
        flow = Flow(
            name="valid_ref",
            common_input=["x"],
            item_output=["y"],
        )
        flow.resource("my_data", MockDbResource(dsn="host/db"))
        flow.recall_static(
            resource_name="my_data",
            item_output=["y"],
            items=[],
        )

        # Should not raise
        cfg = compile_flow(flow)
        assert "resource_config" in cfg

    def test_operator_without_resource_name(self):
        """Operators without resource_name should not trigger validation."""
        flow = Flow(
            name="no_ref",
            common_input=["x"],
            item_output=["y"],
        )
        flow.recall_static(item_output=["y"], items=[])

        # Should not raise even without any resources
        cfg = compile_flow(flow)
        assert "resource_config" not in cfg
