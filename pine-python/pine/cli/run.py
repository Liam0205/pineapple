from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

from pine.go_format import go_json_marshal_indent


def main():
    from pine.operators import ensure_registered
    ensure_registered()

    config_path = ""
    request_path = ""
    resources_path = ""

    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "-config" and i + 1 < len(args):
            i += 1
            config_path = args[i]
        elif args[i] == "-request" and i + 1 < len(args):
            i += 1
            request_path = args[i]
        elif args[i] == "-static-resources" and i + 1 < len(args):
            i += 1
            resources_path = args[i]
        i += 1

    if not config_path or not request_path:
        print(
            "Usage: RunCli -config <pipeline.json> -request <request.json> "
            "[-static-resources <resources.json>]",
            file=sys.stderr,
        )
        sys.exit(1)

    try:
        config_data = Path(config_path).read_bytes()
    except IOError as e:
        print(f"error reading config: {e}", file=sys.stderr)
        sys.exit(1)

    try:
        request_data = Path(request_path).read_bytes()
    except IOError as e:
        print(f"error reading request: {e}", file=sys.stderr)
        sys.exit(1)

    resource_provider = None
    if resources_path:
        try:
            res_data = Path(resources_path).read_bytes()
            resources = json.loads(res_data)
            from pine.engine import StaticResourceProvider
            resource_provider = StaticResourceProvider(resources)
        except IOError as e:
            print(f"error reading static resources: {e}", file=sys.stderr)
            sys.exit(1)

    from pine.engine import Engine
    from pine.errors import ConfigError, RegistryError

    try:
        engine = Engine.create(config_data, resource_provider=resource_provider)
    except (ConfigError, RegistryError) as e:
        print(f"error creating engine: {e}", file=sys.stderr)
        sys.exit(1)

    try:
        req = json.loads(request_data)
    except json.JSONDecodeError as e:
        print(f"error parsing request: {e}", file=sys.stderr)
        sys.exit(1)

    common = req.get("common", {})
    items = req.get("items", [])

    result = engine.execute(common, items)

    if result.error is not None:
        print(f"execution error: {result.error}", file=sys.stderr)
        sys.exit(1)

    output: dict[str, Any] = {}
    output["common"] = result.common
    output["items"] = result.items

    json_str = go_json_marshal_indent(output)
    print(json_str)


if __name__ == "__main__":
    main()
