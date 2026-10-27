#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 23. Embedded-API request-level debug parity ----------
echo
echo "==> [23/$TOTAL_SECTIONS] Embedded request-level debug parity (Emitter / Recorder, issue #219)"
echo "    Each runtime's probe runs the same scenarios through its engine API"
echo "    and prints the result plus its sorted debug events; stdout must be"
echo "    byte-identical:"
echo "      no_emitter         no emitter attached → no events, results unchanged"
echo "      node_meta          operator / type_name / direct SubFlow, incl. a nested SubFlow"
echo "      data_parallel      one event per shard, all with the node's meta"
echo "      panicking_emitter  a failing emitter is isolated → business result unchanged"
echo "    Every event also reports whether a request value survived node binding."

GO_OUT=$("$WORK_DIR/pine-debug-probe" 2>&1 || true)
echo "$GO_OUT" | sed 's/^/      go:   /'

probe_total=0
probe_pass=0

probe_check() {
  local label="$1"
  local output="$2"
  probe_total=$((probe_total + 1))
  if [[ "$output" == "$GO_OUT" ]]; then
    probe_pass=$((probe_pass + 1))
    echo "    [$label] byte-identical to Go"
  else
    fail "embedded request debug: $label differs from Go"
    diff <(echo "$GO_OUT") <(echo "$output") | sed 's/^/      /' >&2 || true
  fi
}

# Guard against a probe that prints nothing (or crashed before the first
# scenario) passing by matching another runtime's equally empty output, and
# against a recorder that silently never reaches the operators.
if [[ $(grep -c '^scenario=[a-z_]* error=' <<< "$GO_OUT") -ne 4 ]]; then
  fail "embedded request debug: Go probe did not print 4 scenarios"
fi
if [[ $(grep -c '^scenario=[a-z_]* event=' <<< "$GO_OUT") -ne 12 ]]; then
  fail "embedded request debug: Go probe did not print 12 events"
fi

JAVA_OUT=$(java -cp "$JAVA_CP" page.liam.pine.DebugProbe 2>&1 || true)
probe_check "java" "$JAVA_OUT"

if [[ -n "${CPP_DEBUG_PROBE:-}" && -x "$CPP_DEBUG_PROBE" ]]; then
  CPP_OUT=$("$CPP_DEBUG_PROBE" 2>&1 || true)
  probe_check "cpp" "$CPP_OUT"
fi

if [[ $probe_total -gt 0 && $probe_pass -eq $probe_total ]]; then
  pass "embedded request debug parity ($probe_pass/$probe_total probes byte-identical to Go)"
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
