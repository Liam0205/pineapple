#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 15. Cause chain parity ----------
echo
echo "==> [15/$TOTAL_SECTIONS] Cause chain parity (ExecutionError unwrap idiom)"
echo "    Verifies that wrapping an inner exception via the runtime's"
echo "    ExecutionError preserves the inner object so downstream code can"
echo "    recover it via the language's native unwrap idiom:"
echo "      Go     errors.As(outer, &inner)"
echo "      Java   outer.getCause() instanceof Inner"
echo "      Python isinstance(outer.__cause__, Inner)"
echo "      C++    pine::error_as<Inner>(outer)"

EXPECTED="PASS:key=user:42 not found"

probe_total=0
probe_pass=0

probe_check() {
  local label="$1"
  local output="$2"
  probe_total=$((probe_total + 1))
  if [[ "$output" == "$EXPECTED" ]]; then
    probe_pass=$((probe_pass + 1))
    echo "    [$label] cause chain → $output"
  else
    fail "cause chain: $label expected '$EXPECTED', got '$output'"
  fi
}

# Go probe
GO_OUT=$("$WORK_DIR/pine-cause-chain-probe" 2>&1 || true)
probe_check "go" "$GO_OUT"

# Java probe
JAVA_OUT=$(java -cp "$JAVA_CP" page.liam.pine.CauseChainProbe 2>&1 || true)
probe_check "java" "$JAVA_OUT"

# Python probe
PY_OUT=$(cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.cause_chain_probe 2>&1 || true)
probe_check "python" "$PY_OUT"

# C++ probe (gated on prebuild)
if [[ -n "${CPP_CAUSE_CHAIN_PROBE:-}" && -x "$CPP_CAUSE_CHAIN_PROBE" ]]; then
  CPP_OUT=$("$CPP_CAUSE_CHAIN_PROBE" 2>&1 || true)
  probe_check "cpp" "$CPP_OUT"
fi

if [[ $probe_total -gt 0 && $probe_pass -eq $probe_total ]]; then
  pass "cause chain parity ($probe_pass/$probe_total probes byte-identical)"
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
