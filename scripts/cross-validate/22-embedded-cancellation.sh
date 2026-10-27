#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 22. Embedded-API cancellation parity ----------
echo
echo "==> [22/$TOTAL_SECTIONS] Embedded cancellation parity (RequestContext / ctx, issue #217)"
echo "    Each runtime's probe runs the same scenarios through its engine API"
echo "    with a cancelled or timing-out context; stdout must be byte-identical:"
echo "      pre_cancel        context done before the run → CancelledError, nothing ran"
echo "      cancel_mid_run    op_a cancels and completes, op_b skipped → CancelledError, x kept"
echo "      cancel_last_node  cancel while the only node runs → success"
echo "      deadline_sleep    bench_sleep stops at the deadline → ExecutionError naming it"
echo "      cancel_sleep      bench_sleep stops on a cancel from another thread"

GO_OUT=$("$WORK_DIR/pine-cancel-probe" 2>&1 || true)
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
    fail "embedded cancellation: $label differs from Go"
    diff <(echo "$GO_OUT") <(echo "$output") | sed 's/^/      /' >&2 || true
  fi
}

# Guard against a probe that prints nothing (or crashed before the first
# scenario) passing by matching another runtime's equally empty output.
if [[ $(grep -c '^scenario=' <<< "$GO_OUT") -ne 5 ]]; then
  fail "embedded cancellation: Go probe did not print 5 scenarios"
fi

JAVA_OUT=$(java -cp "$JAVA_CP" page.liam.pine.CancelProbe 2>&1 || true)
probe_check "java" "$JAVA_OUT"

if [[ -n "${CPP_CANCEL_PROBE:-}" && -x "$CPP_CANCEL_PROBE" ]]; then
  CPP_OUT=$("$CPP_CANCEL_PROBE" 2>&1 || true)
  probe_check "cpp" "$CPP_OUT"
fi

if [[ $probe_total -gt 0 && $probe_pass -eq $probe_total ]]; then
  pass "embedded cancellation parity ($probe_pass/$probe_total probes byte-identical to Go)"
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
