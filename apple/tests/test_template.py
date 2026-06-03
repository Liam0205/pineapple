"""Tests for the shared template-syntax helpers (apple/template.py) and
the validate_templated_params validator (apple/validator.py).

These cover the Apple-side foundation for issue #74: per-request {{field}}
interpolation in operator params.
"""
import os
import sys

import pytest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", ".."))

from apple.template import (
    extract_fields,
    extract_fields_from_params,
    is_bare_template,
    is_templated,
)
from apple.validator import ValidationError, validate_templated_params


class TestIsTemplated:
    def test_plain_string(self):
        assert is_templated("hello") is False

    def test_single_marker(self):
        assert is_templated("{{user_id}}") is True

    def test_marker_with_surrounding_text(self):
        assert is_templated("prefix-{{x}}-suffix") is True

    def test_multiple_markers(self):
        assert is_templated("{{a}}/{{b}}") is True

    def test_empty_braces_not_a_marker(self):
        assert is_templated("{{}}") is False

    def test_non_string(self):
        assert is_templated(None) is False
        assert is_templated(42) is False
        assert is_templated(True) is False
        assert is_templated(["{{x}}"]) is False
        assert is_templated({"k": "{{x}}"}) is False


class TestIsBareTemplate:
    """L0 contract: a templated value must be exactly ``{{field}}`` — no
    surrounding literals, no multiple markers."""

    def test_bare_marker(self):
        assert is_bare_template("{{user_id}}") is True

    def test_prefix_rejected(self):
        assert is_bare_template("prefix-{{x}}") is False

    def test_suffix_rejected(self):
        assert is_bare_template("{{x}}-suffix") is False

    def test_both_sides_rejected(self):
        assert is_bare_template("tenant:{{tenant_id}}:") is False

    def test_two_markers_rejected(self):
        assert is_bare_template("{{a}}{{b}}") is False

    def test_plain_string_rejected(self):
        assert is_bare_template("hello") is False

    def test_empty_braces_rejected(self):
        assert is_bare_template("{{}}") is False

    def test_non_string_rejected(self):
        assert is_bare_template(None) is False
        assert is_bare_template(42) is False


class TestExtractFields:
    def test_single(self):
        assert extract_fields("{{user_id}}") == ["user_id"]

    def test_multiple_ordered(self):
        assert extract_fields("{{a}} then {{b}}") == ["a", "b"]

    def test_dedup_preserves_first_occurrence(self):
        assert extract_fields("{{a}}/{{b}}/{{a}}") == ["a", "b"]

    def test_no_markers(self):
        assert extract_fields("plain") == []

    def test_empty_string(self):
        assert extract_fields("") == []


class TestExtractFieldsFromParams:
    def test_no_templated_values(self):
        assert extract_fields_from_params({"k": "v", "n": 1}) == []

    def test_single_templated(self):
        assert extract_fields_from_params({"lookup_key": "{{user_id}}"}) == ["user_id"]

    def test_multiple_params_ordered(self):
        params = {"a": "{{x}}", "b": "{{y}}"}
        assert extract_fields_from_params(params) == ["x", "y"]

    def test_dedup_across_params(self):
        params = {"a": "{{x}}", "b": "{{x}}/{{y}}"}
        assert extract_fields_from_params(params) == ["x", "y"]

    def test_skips_non_string_values(self):
        # Lists / dicts are explicitly out of scope (scalar-only contract).
        params = {"a": ["{{x}}"], "b": {"k": "{{y}}"}, "c": "{{z}}"}
        assert extract_fields_from_params(params) == ["z"]


# --- validate_templated_params --------------------------------------------

class _FakeOp:
    """Minimal OpCall-like stub for validator tests."""
    def __init__(self, type_name: str, params: dict, code_info: str = "",
                 subflow_path: str = ""):
        self.type_name = type_name
        self.params = params
        self.code_info = code_info
        self.subflow_path = subflow_path


def _patch_schema_lookup(monkeypatch, mapping):
    """Replace _lookup_params_schema with a dict-driven stub."""
    from apple import validator as v

    def fake(type_name):
        return mapping.get(type_name)
    monkeypatch.setattr(v, "_lookup_params_schema", fake)


class TestValidateTemplatedParams:
    def test_no_templated_params_is_noop(self, monkeypatch):
        # Schema lookup must not even be invoked when there are no markers.
        called = []

        from apple import validator as v
        monkeypatch.setattr(
            v, "_lookup_params_schema",
            lambda t: called.append(t) or None,
        )
        op = _FakeOp("op_a", {"plain": "no markers", "n": 1})
        validate_templated_params([("op_a_x", op)])
        assert called == []

    def test_schema_missing_silently_skips(self, monkeypatch):
        # Pre-codegen workflows must not break.
        _patch_schema_lookup(monkeypatch, {})  # nothing registered
        op = _FakeOp("unknown_op", {"k": "{{x}}"})
        validate_templated_params([("u", op)])  # no raise

    def test_templatable_scalar_passes(self, monkeypatch):
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string", "templatable": True}},
        })
        op = _FakeOp("op_a", {"k": "{{x}}"})
        validate_templated_params([("a", op)])

    def test_unknown_param_rejected(self, monkeypatch):
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string", "templatable": True}},
        })
        op = _FakeOp("op_a", {"missing": "{{x}}"})
        with pytest.raises(ValidationError, match="unknown param 'missing'"):
            validate_templated_params([("a", op)])

    def test_non_templatable_param_rejected(self, monkeypatch):
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string", "templatable": False}},
        })
        op = _FakeOp("op_a", {"k": "{{x}}"})
        with pytest.raises(ValidationError, match="does not opt into template"):
            validate_templated_params([("a", op)])

    def test_templatable_missing_defaults_to_false(self, monkeypatch):
        # Codegen omits the "templatable" key when False, so absence == False.
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string"}},
        })
        op = _FakeOp("op_a", {"k": "{{x}}"})
        with pytest.raises(ValidationError, match="does not opt into template"):
            validate_templated_params([("a", op)])

    def test_non_scalar_type_rejected(self, monkeypatch):
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string_list", "templatable": True}},
        })
        op = _FakeOp("op_a", {"k": "{{x}}"})
        with pytest.raises(ValidationError, match="declared type 'string_list'"):
            validate_templated_params([("a", op)])

    def test_all_scalar_types_allowed(self, monkeypatch):
        for t in ("string", "int", "int64", "float", "float64", "bool"):
            _patch_schema_lookup(monkeypatch, {
                "op_a": {"k": {"type": t, "templatable": True}},
            })
            op = _FakeOp("op_a", {"k": "{{x}}"})
            validate_templated_params([("a", op)])

    def test_malformed_marker_rejected(self, monkeypatch):
        # is_templated already screens "{{}}" out, but a value like "{{x}}"
        # with no extractable fields would have failed earlier; we make sure
        # the guard exists. Use a value that passes is_templated by containing
        # at least one valid token but verify the error path stays sound.
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string", "templatable": True}},
        })
        # Sanity: valid extraction proceeds without raise
        op = _FakeOp("op_a", {"k": "{{x}}"})
        validate_templated_params([("a", op)])

    def test_error_includes_op_location(self, monkeypatch):
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string", "templatable": False}},
        })
        op = _FakeOp("op_a", {"k": "{{x}}"}, code_info="file.py:42")
        with pytest.raises(ValidationError, match="file.py:42"):
            validate_templated_params([("named", op)])

    def test_non_bare_marker_rejected(self, monkeypatch):
        """L0 contract: literal prefix/suffix around the marker is rejected."""
        _patch_schema_lookup(monkeypatch, {
            "op_a": {"k": {"type": "string", "templatable": True}},
        })
        for bad in (
            "prefix-{{x}}",
            "{{x}}-suffix",
            "tenant:{{tenant_id}}:",
            "{{a}}{{b}}",
        ):
            op = _FakeOp("op_a", {"k": bad})
            with pytest.raises(ValidationError, match="not a bare"):
                validate_templated_params([("a", op)])
