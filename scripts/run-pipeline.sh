#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<EOF
Usage: $0 --backend go|java -config <pipeline.json> -request <request.json>

Execute a pipeline with the specified runtime and print the result JSON to stdout.

Examples:
  $0 --backend go -config pipeline.json -request req.json
  $0 --backend java -config pipeline.json -request req.json
EOF
  exit 1
}

[[ $# -lt 5 ]] && usage

BACKEND=""
CONFIG=""
REQUEST=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --backend) BACKEND="$2"; shift 2 ;;
    -config)   CONFIG="$2"; shift 2 ;;
    -request)  REQUEST="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; usage ;;
  esac
done

[[ -z "$BACKEND" || -z "$CONFIG" || -z "$REQUEST" ]] && usage

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG_ABS="$(cd "$(dirname "$CONFIG")" && pwd)/$(basename "$CONFIG")"
REQUEST_ABS="$(cd "$(dirname "$REQUEST")" && pwd)/$(basename "$REQUEST")"

case "$BACKEND" in
  go)
    cd "$REPO_ROOT/pine-go"
    go run ./cmd/pineapple-run -config "$CONFIG_ABS" -request "$REQUEST_ABS"
    ;;
  java)
    cd "$REPO_ROOT/pine-java"
    mvn -B -q compile exec:java \
      -Dexec.mainClass="page.liam.pine.RunCli" \
      -Dexec.args="-config $CONFIG_ABS -request $REQUEST_ABS" 2>/dev/null
    ;;
  *)
    echo "Unknown backend: $BACKEND" >&2; usage ;;
esac
