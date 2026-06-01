#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $0 <pipeline.json> [options]

Render the DAG of a compiled pipeline config.

Options:
  --backend go|java   Which runtime to use (default: go)
  -f, --format FORMAT   Output format: dot (default) or mermaid
  -c, --collapse LEVEL  SubFlow collapse level (0 = full graph)
  -o, --output FILE     Write output to file instead of stdout

Examples:
  $0 pipeline.json
  $0 pipeline.json -f mermaid -c 1
  $0 pipeline.json --backend java -f dot
  $0 pipeline.json -f dot | dot -Tpng -o dag.png
EOF
  exit 1
}

[[ $# -lt 1 ]] && usage

CONFIG="$1"; shift
FORMAT="dot"
COLLAPSE=0
OUTPUT=""
BACKEND="go"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --backend)    BACKEND="$2"; shift 2 ;;
    -f|--format)  FORMAT="$2"; shift 2 ;;
    -c|--collapse) COLLAPSE="$2"; shift 2 ;;
    -o|--output)  OUTPUT="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG_ABS="$(cd "$(dirname "$CONFIG")" && pwd)/$(basename "$CONFIG")"

case "$BACKEND" in
  go)
    RESULT=$(cd "$REPO_ROOT/pine-go" && go run ./cmd/pineapple-dag \
      -config "$CONFIG_ABS" \
      -format "$FORMAT" \
      -collapse "$COLLAPSE")
    ;;
  java)
    RESULT=$(cd "$REPO_ROOT/pine-java" && mvn -B -q compile exec:java \
      -Dexec.mainClass="page.liam.pine.RenderDAGCli" \
      -Dexec.args="-config $CONFIG_ABS -format $FORMAT -collapse $COLLAPSE" \
      2>/dev/null)
    ;;
  *)
    echo "Unknown backend: $BACKEND" >&2; usage ;;
esac

if [[ -n "$OUTPUT" ]]; then
  echo "$RESULT" > "$OUTPUT"
  echo "==> DAG written to $OUTPUT (backend: $BACKEND)" >&2
else
  echo "$RESULT"
fi
