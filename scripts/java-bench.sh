#!/usr/bin/env bash
set -euo pipefail
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT/pine-java"

mvn test -B \
  -Dtest=PipelineFixtureTest \
  -DfailIfNoTests=false \
  -Dsurefire.useFile=false \
  -Drepeat=10 \
  "$@"

echo
echo "Note: pine-java does not yet have JMH. This runs fixture tests"
echo "repeatedly as a rough throughput indicator. Add JMH for proper benchmarks."
