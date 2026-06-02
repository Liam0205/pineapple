#include "pine/operator.hpp"
#include "pine/pine.hpp"
#include "pine/resource.hpp"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstring>
#include <fstream>
#include <iostream>
#include <limits>
#include <sstream>
#include <string>
#include <vector>

namespace {

// --- Python literal formatters (byte-aligned with Go pythonLiteral / Java toPythonLiteral) ---

std::string python_escape(const std::string& s) {
  std::ostringstream out;
  for (char ch : s) {
    auto uc = static_cast<unsigned char>(ch);
    switch (ch) {
      case '\\':
        out << "\\\\";
        break;
      case '"':
        out << "\\\"";
        break;
      case '\n':
        out << "\\n";
        break;
      case '\r':
        out << "\\r";
        break;
      case '\t':
        out << "\\t";
        break;
      case '\b':
        out << "\\b";
        break;
      case '\f':
        out << "\\f";
        break;
      default:
        if (uc < 0x20 || (uc >= 0x7f && uc <= 0x9f)) {
          char buf[8];
          std::snprintf(buf, sizeof(buf), "\\u%04x", uc);
          out << buf;
        } else {
          out << ch;
        }
    }
  }
  return out.str();
}

// Mirror Go fmt.Sprintf("%g", d): shortest round-trippable representation that
// Python parses identically. Java's GoFormat.formatG is the reference impl.
//
// Scope: in practice the only doubles that flow through here are operator/
// resource ParamSchema default values declared in C++ source — all current
// callsites use small integer-valued defaults (e.g. db=0, default_value=-1.0).
// Two limitations to be aware of if that ever changes:
//
//   (1) The integer fast path guards against UB by checking std::isfinite and
//       that |d| fits in long long before the static_cast. NaN/Inf and
//       out-of-range magnitudes (|d| > LLONG_MAX) fall through to the
//       printf branch instead of being cast.
//
//   (2) The fallback uses printf("%g", d) with the default 6-digit precision,
//       which is NOT strictly equivalent to Go's shortest-round-trippable
//       %g. For the small defaults we serialise today this matches Go
//       byte-for-byte; a future default with >6 significant digits would
//       diverge and need a proper shortest-round-trippable algorithm
//       (Ryu / Grisu) to stay parity-safe.
std::string format_g(double d) {
  // LLONG_MAX itself is not exactly representable as double; the conversion
  // rounds up to 2^63, which is the smallest double that would overflow the
  // static_cast. Using strict < against the rounded bound keeps every value
  // that survives the comparison castable without UB.
  constexpr double kLLongMaxAsDouble = static_cast<double>(std::numeric_limits<long long>::max());
  if (std::isfinite(d) && std::abs(d) < kLLongMaxAsDouble &&
      d == static_cast<double>(static_cast<long long>(d))) {
    return std::to_string(static_cast<long long>(d));
  }
  char buf[64];
  std::snprintf(buf, sizeof(buf), "%g", d);
  return buf;
}

std::string python_literal(const pine::Variant& v) {
  if (v.is_null()) {
    return "None";
  }
  if (v.is_bool()) {
    return v.as_bool() ? "True" : "False";
  }
  if (v.is_string()) {
    return "\"" + python_escape(v.as_string()) + "\"";
  }
  if (v.is_number()) {
    return format_g(v.as_number());
  }
  return "None";
}

std::string python_type(const std::string& go_type) {
  if (go_type == "string") {
    return "str";
  }
  if (go_type == "int" || go_type == "int64") {
    return "int";
  }
  if (go_type == "float64") {
    return "float";
  }
  if (go_type == "bool") {
    return "bool";
  }
  return "Any";
}

std::string python_default_for_type(const std::string& go_type) {
  if (go_type == "string") {
    return "\"\"";
  }
  if (go_type == "int" || go_type == "int64") {
    return "0";
  }
  if (go_type == "float64") {
    return "0.0";
  }
  if (go_type == "bool") {
    return "False";
  }
  return "None";
}

std::string camel_case(const std::string& s) {
  std::string out;
  out.reserve(s.size());
  bool upper = true;
  for (char ch : s) {
    if (ch == '_') {
      upper = true;
      continue;
    }
    if (upper && ch >= 'a' && ch <= 'z') {
      out.push_back(static_cast<char>(ch - 'a' + 'A'));
      upper = false;
    } else {
      out.push_back(ch);
      upper = false;
    }
  }
  return out;
}

// Op type name, lowercase: e.g. "recall" / "transform". Mirrors Go op_type_to_string.
// (Unused in current templates — recall branching uses OpType::Recall directly — but
// kept here as the canonical lowercase conversion if a future template needs it.)
[[maybe_unused]] std::string lc_op_type(pine::OpType t) {
  return pine::op_type_to_string(t);
}

std::vector<pine::OperatorEntry> all_operator_entries() {
  std::vector<pine::OperatorEntry> out;
  for (const auto& name : pine::registered_operator_names()) {
    if (const auto* e = pine::registry_entry(name); e != nullptr) {
      out.push_back(*e);
    }
  }
  std::sort(out.begin(), out.end(), [](const pine::OperatorEntry& a, const pine::OperatorEntry& b) {
    return a.schema.name < b.schema.name;
  });
  return out;
}

void write_operators_py(std::ostream& out, const std::vector<pine::OperatorEntry>& entries) {
  out << "# auto-generated from pine operator schema \xe2\x80\x94 DO NOT EDIT\n";
  out << "from __future__ import annotations\n";
  out << "from typing import Any\n";
  out << "from apple.base import BaseOp\n";
  out << "\n";

  for (const auto& entry : entries) {
    const auto& schema = entry.schema;
    const std::string cls = camel_case(schema.name) + "Op";

    out << "\n";
    out << "class " << cls << "(BaseOp):\n";
    out << "    \"\"\"Operator: " << schema.name << "\"\"\"\n";
    out << "    _name = \"" << schema.name << "\"\n";

    out << "    _params_schema = {";
    for (const auto& [pname, pspec] : schema.params) {
      out << "\n        \"" << pname << "\": {\"type\": \"" << pspec.type
          << "\", \"required\": " << (pspec.required ? "True" : "False");
      if (!pspec.default_value.is_null()) {
        out << ", \"default\": " << python_literal(pspec.default_value);
      }
      out << "},";
    }
    out << "\n    }\n";

    out << "\n    def __call__(\n";
    out << "        self,\n";
    out << "        *,\n";
    for (const auto& [pname, pspec] : schema.params) {
      std::string py_type = python_type(pspec.type);
      std::string py_default;
      if (pspec.required) {
        py_default = "...";
      } else if (!pspec.default_value.is_null()) {
        py_default = python_literal(pspec.default_value);
      } else {
        py_default = python_default_for_type(pspec.type);
      }
      out << "        " << pname << ": " << py_type << " = " << py_default << ",\n";
    }
    out << "        common_input: list[str] | None = None,\n";
    out << "        common_output: list[str] | None = None,\n";
    out << "        item_input: list[str] | None = None,\n";
    out << "        item_output: list[str] | None = None,\n";
    out << "        item_defaults: dict | None = None,\n";
    out << "        common_defaults: dict | None = None,\n";
    out << "        consumes_row_set: bool = False,\n";
    out << "        debug: bool = False,\n";
    out << "        name: str | None = None,\n";
    out << "    ) -> \"" << cls << "\":\n";

    out << "        _params = {\n";
    for (const auto& [pname, pspec] : schema.params) {
      if (pspec.required || !pspec.default_value.is_null()) {
        out << "            \"" << pname << "\": " << pname << ",\n";
      }
    }
    out << "        }\n";
    for (const auto& [pname, pspec] : schema.params) {
      if (!pspec.required && pspec.default_value.is_null()) {
        out << "        if " << pname << " is not None:\n";
        out << "            _params[\"" << pname << "\"] = " << pname << "\n";
      }
    }

    out << "        return self._apply(\n";
    out << "            params=_params,\n";
    out << "            common_input=common_input,\n";
    out << "            common_output=common_output,\n";
    out << "            item_input=item_input,\n";
    out << "            item_output=item_output,\n";
    out << "            item_defaults=item_defaults,\n";
    out << "            common_defaults=common_defaults,\n";
    if (schema.type == pine::OpType::Recall) {
      out << "            recall=True,\n";
    }
    out << "            consumes_row_set=consumes_row_set,\n";
    out << "            debug=debug,\n";
    out << "            name=name or \"\",\n";
    out << "        )\n";
  }
}

void write_init_py(std::ostream& out, const std::vector<pine::OperatorEntry>& entries) {
  out << "# auto-generated from pine operator schema \xe2\x80\x94 DO NOT EDIT\n";
  for (const auto& entry : entries) {
    out << "from .operators import " << camel_case(entry.schema.name) << "Op\n";
  }
  out << "\n";
  out << "__all__ = [";
  for (const auto& entry : entries) {
    out << "\"" << camel_case(entry.schema.name) << "Op\", ";
  }
  out << "]\n";
}

void write_markers_py(std::ostream& out, const std::vector<pine::OperatorEntry>& entries) {
  out << "# auto-generated from pine operator schema \xe2\x80\x94 DO NOT EDIT\n";
  out << "\"\"\"Row-set marker bools per operator, probed from Go factories at codegen time.\n";
  out << "\n";
  out << "The Go side declares row-set semantics via marker interfaces\n";
  out << "(AdditiveWritesRowSet, ConsumesRowSet, MutatesRowSet). This file mirrors\n";
  out << "those flags so Apple OpCall and the validator can judge row-set behavior\n";
  out << "directly instead of inferring from operator name prefix.\n";
  out << "\"\"\"\n";
  out << "from __future__ import annotations\n";
  out << "\n";
  out << "OPERATOR_MARKERS: dict[str, dict[str, bool]] = {\n";
  for (const auto& entry : entries) {
    out << "    \"" << entry.schema.name << "\": {\n";
    out << "        \"additive_writes_row_set\": " << (entry.additive_writes_row_set ? "True" : "False")
        << ",\n";
    out << "        \"consumes_row_set\": " << (entry.consumes_row_set ? "True" : "False") << ",\n";
    out << "        \"mutates_row_set\": " << (entry.mutates_row_set ? "True" : "False") << ",\n";
    out << "    },\n";
  }
  out << "}\n";
  out << "\n";
  out << "\n";
  out << "def get_markers(type_name: str) -> dict[str, bool]:\n";
  out << "    \"\"\"Return the marker dict for type_name, or all-False defaults if unknown.\n";
  out << "\n";
  out << "    Unknown operators (e.g., custom ops registered after codegen) are\n";
  out << "    treated as having no row-set semantics; the Go side remains authoritative.\n";
  out << "    \"\"\"\n";
  out << "    return OPERATOR_MARKERS.get(type_name, {\n";
  out << "        \"additive_writes_row_set\": False,\n";
  out << "        \"consumes_row_set\": False,\n";
  out << "        \"mutates_row_set\": False,\n";
  out << "    })\n";
}

void write_resources_py(std::ostream& out, const std::vector<pine::resource::ResourceSchema>& schemas) {
  out << "# auto-generated from pine resource schema \xe2\x80\x94 DO NOT EDIT\n";
  out << "from __future__ import annotations\n";
  out << "from typing import Any\n";
  out << "from apple.resource import BaseResource\n";
  out << "\n";

  for (const auto& schema : schemas) {
    const std::string cls = camel_case(schema.name) + "Resource";
    out << "\n";
    out << "class " << cls << "(BaseResource):\n";
    if (!schema.description.empty()) {
      out << "    \"\"\"Resource: " << schema.name << " \xe2\x80\x94 " << schema.description << "\"\"\"\n";
    } else {
      out << "    \"\"\"Resource: " << schema.name << "\"\"\"\n";
    }
    out << "    _name = \"" << schema.name << "\"\n";
    out << "    _default_interval = " << schema.default_interval << "\n";

    out << "    _params_schema = {";
    for (const auto& [pname, pspec] : schema.params) {
      out << "\n        \"" << pname << "\": {\"type\": \"" << pspec.type
          << "\", \"required\": " << (pspec.required ? "True" : "False");
      if (!pspec.default_value.is_null()) {
        out << ", \"default\": " << python_literal(pspec.default_value);
      }
      out << "},";
    }
    out << "\n    }\n";

    out << "\n    def __init__(\n";
    out << "        self,\n";
    out << "        *,\n";
    for (const auto& [pname, pspec] : schema.params) {
      std::string py_type = python_type(pspec.type);
      std::string py_default;
      if (pspec.required) {
        py_default = "...";
      } else if (!pspec.default_value.is_null()) {
        py_default = python_literal(pspec.default_value);
      } else {
        py_default = python_default_for_type(pspec.type);
      }
      out << "        " << pname << ": " << py_type << " = " << py_default << ",\n";
    }
    out << "        interval: int = " << schema.default_interval << ",\n";
    out << "    ):\n";
    out << "        super().__init__(\n";
    out << "            interval=interval,\n";
    for (const auto& [pname, _] : schema.params) {
      out << "            " << pname << "=" << pname << ",\n";
    }
    out << "        )\n";
  }
}

void write_resources_init_py(std::ostream& out, const std::vector<pine::resource::ResourceSchema>& schemas) {
  out << "# auto-generated from pine resource schema \xe2\x80\x94 DO NOT EDIT\n";
  for (const auto& schema : schemas) {
    out << "from .resources import " << camel_case(schema.name) << "Resource\n";
  }
  out << "\n";
  out << "__all__ = [";
  for (const auto& schema : schemas) {
    out << "\"" << camel_case(schema.name) << "Resource\", ";
  }
  out << "]\n";
}

bool write_file(const std::string& path, const std::string& contents) {
  std::ofstream f(path);
  if (!f) {
    std::cerr << "Error: cannot open file for writing: " << path << "\n";
    return false;
  }
  f << contents;
  return true;
}

}  // namespace

int main(int argc, char* argv[]) {
  std::string schema_path;
  std::string output_dir;

  for (int i = 1; i < argc; ++i) {
    const char* a = argv[i];
    if ((std::strcmp(a, "-schema-json") == 0 || std::strcmp(a, "--schema-json") == 0) && i + 1 < argc) {
      schema_path = argv[++i];
    } else if ((std::strcmp(a, "-output") == 0 || std::strcmp(a, "--output") == 0) && i + 1 < argc) {
      output_dir = argv[++i];
    }
  }

  if (schema_path.empty() && output_dir.empty()) {
    std::cerr << "Usage: pineapple-codegen [-schema-json <path>] [-output <dir>]\n";
    return 1;
  }

  if (!schema_path.empty()) {
    if (!write_file(schema_path, pine::export_schema_json())) {
      return 1;
    }
  }

  if (!output_dir.empty()) {
    const auto entries = all_operator_entries();
    if (entries.empty()) {
      std::cerr << "Error: no operators registered\n";
      return 1;
    }

    // mkdir -p: rely on POSIX. CMake-side caller (cross-validate) creates the
    // directory; we open files directly. Create paths assuming output_dir exists.
    auto join = [&](const std::string& name) { return output_dir + "/" + name; };

    {
      std::ostringstream oss;
      write_operators_py(oss, entries);
      if (!write_file(join("operators.py"), oss.str())) {
        return 1;
      }
    }
    {
      std::ostringstream oss;
      write_init_py(oss, entries);
      if (!write_file(join("__init__.py"), oss.str())) {
        return 1;
      }
    }
    {
      std::ostringstream oss;
      write_markers_py(oss, entries);
      if (!write_file(join("markers.py"), oss.str())) {
        return 1;
      }
    }

    const auto resources = pine::resource::all_resource_schemas();
    if (!resources.empty()) {
      {
        std::ostringstream oss;
        write_resources_py(oss, resources);
        if (!write_file(join("resources.py"), oss.str())) {
          return 1;
        }
      }
      {
        std::ostringstream oss;
        write_resources_init_py(oss, resources);
        if (!write_file(join("resources_init.py"), oss.str())) {
          return 1;
        }
      }
    }
  }

  return 0;
}
