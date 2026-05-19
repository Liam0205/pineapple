from __future__ import annotations

import json
import math
from decimal import Decimal
from typing import Any


def sprint(v: Any) -> str:
    if v is None:
        return "<nil>"
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, int):
        return str(v)
    if isinstance(v, float):
        if math.copysign(1.0, v) == -1.0 and v == 0.0:
            return "-0"
        if v == math.floor(v) and not math.isinf(v) and abs(v) < 1e6:
            return str(int(v))
        return format_g(v)
    if isinstance(v, list):
        parts = " ".join(sprint(x) for x in v)
        return f"[{parts}]"
    if isinstance(v, str):
        return v
    return str(v)


def format_float_f(d: float) -> str:
    if math.copysign(1.0, d) == -1.0 and d == 0.0:
        return "-0"
    if math.isnan(d):
        return "NaN"
    if d == math.inf:
        return "+Inf"
    if d == -math.inf:
        return "-Inf"
    if d == math.floor(d) and not math.isinf(d) and abs(d) < 1e18:
        return str(int(d))
    s = repr(d)
    if "e" not in s and "E" not in s:
        return s
    return str(Decimal(s).normalize())


def format_g(d: float) -> str:
    if d == 0.0:
        if math.copysign(1.0, d) == -1.0:
            return "-0"
        return "0"
    if math.isnan(d):
        return "NaN"
    if d == math.inf:
        return "+Inf"
    if d == -math.inf:
        return "-Inf"

    s = repr(d)
    if "e" in s or "E" in s:
        s = s.lower()
        e_idx = s.index("e")
        mantissa = s[:e_idx]
        exp_part = s[e_idx + 1:]
        exp_value = int(exp_part)

        if -4 <= exp_value <= 5:
            dec = Decimal(repr(d)).normalize()
            return format(dec, "f").rstrip("0").rstrip(".")

        mantissa = mantissa.rstrip("0").rstrip(".")
        sign = "-" if exp_value < 0 else "+"
        digits = str(abs(exp_value))
        if len(digits) < 2:
            digits = "0" + digits
        return f"{mantissa}e{sign}{digits}"

    # Non-scientific: check if integer part exceeds 6 digits
    negative = s.startswith("-")
    abs_s = s[1:] if negative else s
    dot_pos = abs_s.find(".")
    int_part_len = dot_pos if dot_pos >= 0 else len(abs_s)

    if int_part_len > 6:
        all_digits = abs_s.replace(".", "")
        all_digits = all_digits.rstrip("0") or "0"
        exp = int_part_len - 1
        if len(all_digits) == 1:
            mantissa_result = all_digits
        else:
            mantissa_result = all_digits[0] + "." + all_digits[1:]
        exp_str = str(exp) if exp >= 10 else "0" + str(exp)
        result = f"{mantissa_result}e+{exp_str}"
        return f"-{result}" if negative else result

    # Strip trailing zeros
    if "." in s:
        s = s.rstrip("0").rstrip(".")
    return s


def _go_escape(s: str) -> str:
    result = []
    for ch in s:
        if ch == "<":
            result.append("\\u003c")
        elif ch == ">":
            result.append("\\u003e")
        elif ch == "&":
            result.append("\\u0026")
        elif ch == " ":
            result.append("\\u2028")  # U+2028 LINE SEPARATOR
        elif ch == " ":
            result.append("\\u2029")  # U+2029 PARAGRAPH SEPARATOR
        else:
            result.append(ch)
    return "".join(result)


def _go_encode_string(s: str) -> str:
    encoded = json.dumps(s, ensure_ascii=False)
    inner = encoded[1:-1]
    escaped = _go_escape(inner)
    return f'"{escaped}"'


def _go_encode_value(obj: Any, indent: str = "", current_indent: str = "") -> str:
    if obj is None:
        return "null"
    if isinstance(obj, bool):
        return "true" if obj else "false"
    if isinstance(obj, int):
        return str(obj)
    if isinstance(obj, float):
        if math.isnan(obj) or math.isinf(obj):
            return "null"
        if obj == int(obj) and abs(obj) < 2**53:
            return str(int(obj))
        return repr(obj)
    if isinstance(obj, str):
        return _go_encode_string(obj)
    if isinstance(obj, list):
        if not obj:
            return "[]"
        if indent:
            items = []
            child_indent = current_indent + indent
            for item in obj:
                items.append(child_indent + _go_encode_value(item, indent, child_indent))
            return "[\n" + ",\n".join(items) + "\n" + current_indent + "]"
        else:
            items = [_go_encode_value(item, "", "") for item in obj]
            return "[" + ",".join(items) + "]"
    if isinstance(obj, dict):
        if not obj:
            return "{}"
        sorted_keys = sorted(obj.keys())
        if indent:
            entries = []
            child_indent = current_indent + indent
            for key in sorted_keys:
                k_str = _go_encode_string(key)
                v_str = _go_encode_value(obj[key], indent, child_indent)
                entries.append(f"{child_indent}{k_str}: {v_str}")
            return "{\n" + ",\n".join(entries) + "\n" + current_indent + "}"
        else:
            entries = []
            for key in sorted_keys:
                k_str = _go_encode_string(key)
                v_str = _go_encode_value(obj[key], "", "")
                entries.append(f"{k_str}:{v_str}")
            return "{" + ",".join(entries) + "}"
    return json.dumps(obj)


def go_json_marshal(obj: Any) -> str:
    return _go_encode_value(obj, indent="", current_indent="")


def go_json_marshal_indent(obj: Any) -> str:
    return _go_encode_value(obj, indent="  ", current_indent="")
