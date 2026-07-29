#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 21. storage_mode validation parity (issues #179, #187) ----------
# HISTORY, because this section's assertion has inverted once and the reason
# matters more than the current expectation:
#
#   #179 aligned the DISPATCH direction. pine-go's NewFrame is a switch with
#   `default: newRowFrame`, so only the exact literal "column" selects the column
#   store; pine-java had matched case-insensitively and pine-cpp had fallen
#   through to ColumnFrame, so one config picked different physical storage per
#   runtime. This section was then written to assert that an invalid value is
#   ACCEPTED SILENTLY and yields byte-identical output everywhere.
#
#   #187 reversed that: silence was the other half of the problem, since a typo
#   produced a working engine with the opposite memory and performance profile
#   from the one requested. All three runtimes now REJECT an invalid value at
#   config load. The dispatch rule itself is unchanged — validation makes the
#   invalid value unreachable rather than complicating NewFrame.
#
# So this section now asserts rejection parity, and the old expectation is
# recorded above rather than deleted, because "invalid values are accepted" is
# still what the pre-#187 comments and docs in git history describe.
#
# WHAT THIS SECTION CANNOT DO. The dispatch direction for the values that remain
# VALID is still invisible here: row and column stores are output-equivalent by
# design (section 4 asserts exactly that) and no endpoint reports which store was
# selected, so nothing observable from outside distinguishes them. That half is
# pinned per runtime, at the only place it is observable:
#   pine-go   internal/dataframe/storage_mode_dispatch_test.go
#   pine-java StorageModeDispatchTest
#   pine-cpp  test_row_frame.cpp "only the exact literal \"column\"..."
# and the validation half by storage_mode_validation_test.go /
# StorageModeValidationTest / test_storage_mode_validation.cpp. Each was verified
# red against its own regression.
echo
echo "==> [21/$TOTAL_SECTIONS] storage_mode validation parity (valid accepted, invalid rejected)"

SM_FIXTURE="$REPO_ROOT/fixtures/pipelines/transform_then_filter.json"
sm_pass=0
sm_total=0
cpp_sm_pass=0
cpp_sm_total=0

# Valid values must be accepted and produce byte-identical output; invalid values
# must be rejected by all three. "" and absent are valid: they are pine-go's zero
# value, which its default branch routes to row storage.
run_case() {
  local mode="$1" expect="$2" label="$3" omit="$4"
  local cfg="$WORK_DIR/sm_config_${label}.json"
  local req="$WORK_DIR/sm_req.json"

  python3 -c "
import json
with open('$SM_FIXTURE') as f:
    data = json.load(f)
cfg = data.get('config', {})
if '$omit' == 'omit':
    cfg.pop('storage_mode', None)
else:
    cfg['storage_mode'] = '$mode'
with open('$cfg', 'w') as cf:
    json.dump(cfg, cf)
with open('$req', 'w') as rf:
    json.dump(data['cases'][0]['request'], rf)
" || { fail "storage_mode: could not build config for '$label'"; return; }

  # `|| true` on every capture: this section deliberately runs cases that MUST
  # exit non-zero, and _env.sh sets -e, so an unguarded $(...) would abort the
  # whole script at the first rejected case before its rc was even inspected.
  local go_rc java_rc cpp_rc go_out java_out
  go_rc=0
  go_out=$("$WORK_DIR/pineapple-run" -config "$cfg" -request "$req" 2>/dev/null) || go_rc=$?
  java_rc=0
  java_out=$(java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$cfg" -request "$req" 2>/dev/null) \
    || java_rc=$?
  java_out=$(printf '%s' "$java_out" | grep -v '^\[pine' || true)

  sm_total=$((sm_total + 1))
  if [[ "$expect" == "accept" ]]; then
    if [[ $go_rc -eq 0 && $java_rc -eq 0 && "$go_out" == "$java_out" ]]; then
      sm_pass=$((sm_pass + 1))
    else
      fail "storage_mode '$label': expected both to accept with identical output (go=$go_rc java=$java_rc)"
    fi
  else
    if [[ $go_rc -ne 0 && $java_rc -ne 0 ]]; then
      sm_pass=$((sm_pass + 1))
    else
      fail "storage_mode '$label': expected both to REJECT (go=$go_rc java=$java_rc)"
    fi
  fi

  if [[ -n "${CPP_RUN:-}" ]]; then
    local cpp_out
    cpp_rc=0
    cpp_out=$("$CPP_RUN" -config "$cfg" -request "$req" 2>/dev/null) || cpp_rc=$?
    cpp_sm_total=$((cpp_sm_total + 1))
    if [[ "$expect" == "accept" ]]; then
      if [[ $cpp_rc -eq 0 && "$go_out" == "$cpp_out" ]]; then
        cpp_sm_pass=$((cpp_sm_pass + 1))
      else
        fail "storage_mode '$label': C++ expected to accept with identical output (rc=$cpp_rc)"
      fi
    else
      if [[ $cpp_rc -ne 0 ]]; then
        cpp_sm_pass=$((cpp_sm_pass + 1))
      else
        fail "storage_mode '$label': C++ expected to REJECT (rc=$cpp_rc)"
      fi
    fi
  fi
}

run_case "row"     accept row       ""
run_case "column"  accept column    ""
run_case ""        accept empty     ""
run_case ""        accept absent    omit
run_case "colunm"  reject typo      ""
run_case "Column"  reject wrongcase ""
run_case "COLUMN"  reject upper     ""
run_case "unknown" reject unknown   ""

if [[ $sm_pass -eq $sm_total && $sm_total -gt 0 ]]; then
  pass "storage_mode validation parity Go vs Java ($sm_pass/$sm_total cases)"
fi
if [[ $cpp_sm_total -gt 0 && $cpp_sm_pass -eq $cpp_sm_total ]]; then
  pass "storage_mode validation parity Go vs C++ ($cpp_sm_pass/$cpp_sm_total cases)"
fi

# Propagate failure when run standalone, which is how the repo docs recommend
# debugging a single section.
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
