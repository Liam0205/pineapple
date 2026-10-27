#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 24. _source rewrite exemption parity ----------
echo
echo "==> [24/$TOTAL_SECTIONS] _source rewrite exemption parity (declared-output contract, issue #224)"
echo "    Each runtime's probe runs a merge that rewrites the kept item's _source"
echo "    with its duplicates' tags; stdout must be byte-identical:"
echo "      exempt                   MutatesRowSet + reads _source → rewrite accepted"
echo "      exempt_other_undeclared  other undeclared writes still reported, without _source"
echo "      no_source_input          does not read _source → [_source] rejected"
echo "      no_row_set_markers       not MutatesRowSet → [_source] rejected"

GO_OUT=$("$WORK_DIR/pine-source-merge-probe" 2>&1 || true)
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
    fail "_source rewrite exemption: $label differs from Go"
    diff <(echo "$GO_OUT") <(echo "$output") | sed 's/^/      /' >&2 || true
  fi
}

# Guard against a probe that prints nothing (or crashed early) passing by
# matching another runtime's equally empty output, and against an exemption
# that silently stopped applying in Go itself.
if [[ $(grep -c '^scenario=[a-z_]* error=' <<< "$GO_OUT") -ne 4 ]]; then
  fail "_source rewrite exemption: Go probe did not print 4 scenarios"
fi
if ! grep -qx 'scenario=exempt error=nil items=\[x:a,b;y:c\]' <<< "$GO_OUT"; then
  fail "_source rewrite exemption: Go probe did not accept the exempt rewrite"
fi

JAVA_OUT=$(java -cp "$JAVA_CP" page.liam.pine.SourceMergeProbe 2>&1 || true)
probe_check "java" "$JAVA_OUT"

if [[ -n "${CPP_SOURCE_MERGE_PROBE:-}" && -x "$CPP_SOURCE_MERGE_PROBE" ]]; then
  CPP_OUT=$("$CPP_SOURCE_MERGE_PROBE" 2>&1 || true)
  probe_check "cpp" "$CPP_OUT"
fi

if [[ $probe_total -gt 0 && $probe_pass -eq $probe_total ]]; then
  pass "_source rewrite exemption parity ($probe_pass/$probe_total probes byte-identical to Go)"
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
