#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $0 <script.py> [-o OUTPUT]

Compile an Apple DSL script to JSON pipeline config.

The Python script must define a top-level variable named 'flow' of type Flow.

Options:
  -o, --output FILE   Write JSON to file (default: stdout)

Examples:
  $0 my_pipeline.py
  $0 my_pipeline.py -o pipeline.json
EOF
  exit 1
}

[[ $# -lt 1 ]] && usage

SCRIPT="$1"; shift
OUTPUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--output) OUTPUT="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [[ -f "$REPO_ROOT/.venv/bin/activate" ]]; then
  source "$REPO_ROOT/.venv/bin/activate"
fi

PYTHON_CMD="
import sys, json
sys.path.insert(0, '${REPO_ROOT}')
exec(open('${SCRIPT}').read())
if 'flow' not in dir():
    print('Error: script must define a top-level variable named \"flow\"', file=sys.stderr)
    sys.exit(1)
print(flow.compile())
"

if [[ -n "$OUTPUT" ]]; then
  python3 -c "$PYTHON_CMD" > "$OUTPUT"
  echo "==> Compiled to $OUTPUT" >&2
else
  python3 -c "$PYTHON_CMD"
fi
