#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 21. storage_mode fallback parity (issue #179) ----------
# pine-go's NewFrame switches on a string-typed StorageMode with
# `default: newRowFrame`, so exactly one value — the literal "column" — selects
# the column store and everything else selects the row store. Before #179 the
# other two runtimes disagreed: pine-java matched with equalsIgnoreCase so
# "Column" chose column, and pine-cpp's make_frame fell back to column so a
# misspelling like "colunm" chose column too.
#
# WHAT THIS SECTION CAN AND CANNOT DO — stated plainly, because a check that
# looks like it covers something it does not is worse than no check:
#
#   Row and column stores are output-equivalent by design (section 4 asserts
#   exactly that), and no endpoint reports which store was selected — /stats and
#   /dag both carry no storage field. So NOTHING observable from outside the
#   process distinguishes the two, and this section CANNOT detect the dispatch
#   divergence itself. Reverting either fix leaves it green; verified by mutation.
#
#   The dispatch rule is pinned per runtime, at the only place it is observable:
#     pine-go   internal/dataframe/storage_mode_dispatch_test.go
#     pine-java StorageModeDispatchTest
#     pine-cpp  test_row_frame.cpp "only the exact literal \"column\"..."
#   Each was verified red against its own regression.
#
# What this section DOES add is the property those unit tests cannot express:
# that an invalid storage_mode is accepted silently and produces byte-identical
# output across all three runtimes — i.e. whatever store each picks, the response
# is the same. That is the user-visible half of the contract, and it also guards
# against a future "reject invalid values" change landing in one runtime only.
echo
echo "==> [21/$TOTAL_SECTIONS] storage_mode fallback parity (invalid values accepted, output identical)"

SM_FIXTURE="$REPO_ROOT/fixtures/pipelines/transform_then_filter.json"
sm_pass=0
sm_total=0
cpp_sm_pass=0
cpp_sm_total=0

# "row" and "column" are the valid pair; the rest are the shapes #179 was about —
# a misspelling, a wrong casing, an empty string, and an unrelated word.
for mode in row column colunm Column COLUMN "" unknown; do
  label="${mode:-<empty>}"
  SM_CONFIG="$WORK_DIR/sm_config_${label//[^a-zA-Z0-9]/_}.json"
  SM_REQ="$WORK_DIR/sm_req.json"

  python3 -c "
import json
with open('$SM_FIXTURE') as f:
    data = json.load(f)
cfg = data.get('config', {})
cfg['storage_mode'] = '$mode'
with open('$SM_CONFIG', 'w') as cf:
    json.dump(cfg, cf)
with open('$SM_REQ', 'w') as rf:
    json.dump(data['cases'][0]['request'], rf)
" || { fail "storage_mode: could not build config for '$label'"; continue; }

  sm_total=$((sm_total + 1))
  go_out=$("$WORK_DIR/pineapple-run" -config "$SM_CONFIG" -request "$SM_REQ" 2>/dev/null || echo "RUN_FAILED")
  java_out=$(java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$SM_CONFIG" -request "$SM_REQ" 2>/dev/null \
    | grep -v '^\[pine' || echo "RUN_FAILED")

  if [[ "$go_out" == "RUN_FAILED" ]]; then
    fail "storage_mode '$label': Go run failed — invalid values must be accepted silently"
  elif [[ "$go_out" == "$java_out" ]]; then
    sm_pass=$((sm_pass + 1))
  else
    fail "storage_mode '$label': Go vs Java output differs"
  fi

  if [[ -n "${CPP_RUN:-}" ]]; then
    cpp_sm_total=$((cpp_sm_total + 1))
    cpp_out=$("$CPP_RUN" -config "$SM_CONFIG" -request "$SM_REQ" 2>/dev/null || echo "RUN_FAILED")
    if [[ "$go_out" == "$cpp_out" ]]; then
      cpp_sm_pass=$((cpp_sm_pass + 1))
    else
      fail "storage_mode '$label': Go vs C++ output differs"
    fi
  fi
done

if [[ $sm_pass -eq $sm_total && $sm_total -gt 0 ]]; then
  pass "storage_mode fallback parity Go vs Java ($sm_pass/$sm_total modes)"
fi
if [[ $cpp_sm_total -gt 0 && $cpp_sm_pass -eq $cpp_sm_total ]]; then
  pass "storage_mode fallback parity Go vs C++ ($cpp_sm_pass/$cpp_sm_total modes)"
fi
