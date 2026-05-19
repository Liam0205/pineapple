from __future__ import annotations

import sys
from pathlib import Path


def main():
    from pine.operators import ensure_registered
    ensure_registered()

    config_path = ""
    format_ = "dot"
    collapse = 0

    args = sys.argv[1:]
    i = 0
    while i < len(args):
        if args[i] == "-config" and i + 1 < len(args):
            i += 1
            config_path = args[i]
        elif args[i] == "-format" and i + 1 < len(args):
            i += 1
            format_ = args[i]
        elif args[i] == "-collapse" and i + 1 < len(args):
            i += 1
            collapse = int(args[i])
        i += 1

    if not config_path:
        print(
            "Usage: RenderDAGCli -config <path> [-format dot|mermaid] [-collapse N]",
            file=sys.stderr,
        )
        sys.exit(1)

    try:
        data = Path(config_path).read_bytes()
    except IOError as e:
        print(f"error reading config: {e}", file=sys.stderr)
        sys.exit(1)

    from pine.engine import Engine, StaticResourceProvider
    from pine.errors import ConfigError, RegistryError

    try:
        rp = StaticResourceProvider({})
        engine = Engine.create(data, resource_provider=rp)
    except (ConfigError, RegistryError) as e:
        print(f"error creating engine: {e}", file=sys.stderr)
        sys.exit(1)

    try:
        output = engine.render_dag(format_, collapse)
    except ValueError as e:
        print(f"error rendering DAG: {e}", file=sys.stderr)
        sys.exit(1)

    sys.stdout.write(output)


if __name__ == "__main__":
    main()
