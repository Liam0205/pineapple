#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $0 <pipeline.json> [options]

Render the DAG of a compiled pipeline config.

Options:
  -f, --format FORMAT   Output format: dot (default) or mermaid
  -c, --collapse LEVEL  SubFlow collapse level (0 = full graph)
  -o, --output FILE     Write output to file instead of stdout

Examples:
  $0 pipeline.json
  $0 pipeline.json -f mermaid -c 1
  $0 pipeline.json -f dot | dot -Tpng -o dag.png
EOF
  exit 1
}

[[ $# -lt 1 ]] && usage

CONFIG="$1"; shift
FORMAT="dot"
COLLAPSE=0
OUTPUT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -f|--format)  FORMAT="$2"; shift 2 ;;
    -c|--collapse) COLLAPSE="$2"; shift 2 ;;
    -o|--output)  OUTPUT="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

RESULT=$(cd "$REPO_ROOT/pine-go" && go run ./cmd/pineapple-dag \
  -config "$CONFIG" \
  -format "$FORMAT" \
  -collapse "$COLLAPSE")

if [[ -n "$OUTPUT" ]]; then
  echo "$RESULT" > "$OUTPUT"
  echo "==> DAG written to $OUTPUT" >&2
else
  echo "$RESULT"
fi
