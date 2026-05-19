from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

from pine.registry import Registry


def main():
    from pine.operators import ensure_registered
    ensure_registered()

    output_dir = ""
    schema_json_path = ""
    export_schema = ""
    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "-output" and i + 1 < len(args):
            i += 1
            output_dir = args[i]
        elif args[i] == "-schema-json" and i + 1 < len(args):
            i += 1
            schema_json_path = args[i]
        elif args[i] == "--export-schema" and i + 1 < len(args):
            i += 1
            export_schema = args[i]
        i += 1

    if export_schema or schema_json_path:
        out_path = export_schema or schema_json_path
        schemas = Registry.global_instance().schemas()
        schema_list: list[dict[str, Any]] = []
        for schema in schemas:
            params: dict[str, Any] = {}
            for pname, pspec in schema.params.items():
                params[pname] = {
                    "Type": pspec.type,
                    "Required": pspec.required,
                    "Default": pspec.default_value,
                    "Description": pspec.description,
                }
            schema_list.append({
                "Name": schema.name,
                "Type": schema.type.value,
                "Description": schema.description,
                "Params": params,
            })
        Path(out_path).write_text(
            json.dumps(schema_list, indent=2, ensure_ascii=False)
        )
        return

    if not output_dir:
        print("Usage: Codegen --export-schema <path> | -schema-json <path> | -output <dir>",
              file=sys.stderr)
        sys.exit(1)

    # TODO: codegen Python output (generate operators.py, resources.py, __init__.py)
    print(f"codegen output to {output_dir} not yet implemented", file=sys.stderr)
    sys.exit(1)


if __name__ == "__main__":
    main()
