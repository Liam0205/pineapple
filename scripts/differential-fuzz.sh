#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

ROUNDS="${1:-1000}"
SEED="${2:-}"
ENGINES="${3:-go,python,java}"

echo "==> Differential fuzz: $ENGINES ($ROUNDS rounds)"

# Build Go binary if needed
GO_BIN="$REPO_ROOT/pine-go/pineapple-run"
if [[ ! -x "$GO_BIN" ]]; then
  echo "    Building Go binary..."
  (cd "$REPO_ROOT/pine-go" && go build -o "$GO_BIN" ./cmd/pineapple-run/)
fi

SEED_ARG=""
[[ -n "$SEED" ]] && SEED_ARG="--seed $SEED"

python3 "$REPO_ROOT/scripts/differential-fuzz.py" \
  --rounds "$ROUNDS" \
  --go-bin "$GO_BIN" \
  --engines "$ENGINES" \
  $SEED_ARG \
  "${@:4}"
