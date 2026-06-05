#!/usr/bin/env bash
# Section 18: Apple SubFlow contract failure stderr shape (audit H3)
#
# SubFlow field-list contracts (issue #78) are enforced at Apple compile-time
# only — the runtime engines never see the contract metadata. So this section
# is structurally Apple-only: we run the same set of failing cases that
# test_compiler.py::TestSubFlowContractEnforcement covers as unit tests and
# pin the user-visible ValidationError surface (message prefix + stable
# substring) at the integration layer. Without this section, a refactor that
# changes the wording in apple/validator.py would only be caught by the
# Apple unit suite — fixtures-driven smoke runs, downstream tooling that
# parses these messages, and any "did the compiler error change?" gate
# would silently drift.
#
# Each probe runs an independent python3 -c invocation that builds the
# offending Flow, calls compile_flow, and asserts the raised ValidationError
# stringifies to a message containing a known stable substring. Op names
# carry a 6-hex-digit hash suffix that depends on call-site line numbers, so
# we match against contract/SubFlow-name fragments which are deterministic.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

echo
echo "==> [18/$TOTAL_SECTIONS] Apple SubFlow contract failure stderr (compile-time)"

run_subflow_probe() {
  local label="$1"
  local expected_substr="$2"
  local script="$3"
  local out
  out=$(python3 -c "
import sys
sys.path.insert(0, '$REPO_ROOT')
from apple.flow import Flow, SubFlow
from apple.compiler import compile_flow
from apple.validator import ValidationError

try:
$script
    print('NO_ERROR_RAISED')
except ValidationError as e:
    print('ERROR:' + str(e).replace(chr(10), ' | '))
" 2>&1)
  if [[ "$out" == NO_ERROR_RAISED ]]; then
    fail "subflow-contract $label: compile_flow did not raise"
    return
  fi
  if [[ "$out" != ERROR:* ]]; then
    fail "subflow-contract $label: harness error"
    printf '    output: %s\n' "$out" >&2
    return
  fi
  if [[ "$out" == *"$expected_substr"* ]]; then
    pass "subflow-contract $label: stderr contains expected fragment"
  else
    fail "subflow-contract $label: expected fragment missing"
    printf '    expected substring: %s\n    got: %s\n' "$expected_substr" "$out" >&2
  fi
}

# Probe 1: item_input field outside SubFlow contract.
run_subflow_probe \
  "item-input-missing" \
  "item_input field 'y' not provided by SubFlow 'sf' contract" \
  "
    sf = SubFlow(name='sf', item_input=['x'])
    sf._add_op('transform_by_lua', common_input=[], common_output=[],
               item_input=['y'], item_output=['z'],
               lua_script='function f() return 0 end',
               function_for_item='f', function_for_common='')
    flow = Flow(name='parent', item_input=['x','y'], item_output=['z'], sub_flows=[sf])
    compile_flow(flow)
"

# Probe 2: nested SubFlow inherits outer contract (no own contract).
run_subflow_probe \
  "nested-inherits-outer" \
  "item_input field 'bad' not provided by SubFlow 'outer' contract" \
  "
    inner = SubFlow(name='inner')
    inner._add_op('transform_by_lua', common_input=[], common_output=[],
                  item_input=['bad'], item_output=['z'],
                  lua_script='function f() return 0 end',
                  function_for_item='f', function_for_common='')
    outer = SubFlow(name='outer', item_input=['x'], item_output=['z'])
    outer.add_subflow(inner)
    flow = Flow(name='parent', item_input=['x','bad'], item_output=['z'], sub_flows=[outer])
    compile_flow(flow)
"

# Probe 3: dead op inside SubFlow (output not consumed within scope).
run_subflow_probe \
  "dead-op-inside-subflow" \
  "SubFlow 'sf': dead operators" \
  "
    sf = SubFlow(name='sf', item_input=['x'], item_output=['z'])
    sf._add_op('transform_by_lua', common_input=[], common_output=[],
               item_input=['x'], item_output=['z'],
               lua_script='function f() return 0 end',
               function_for_item='f', function_for_common='')
    sf._add_op('transform_by_lua', name='dead_op', common_input=[], common_output=[],
               item_input=['x'], item_output=['extra'],
               lua_script='function f() return 0 end',
               function_for_item='f', function_for_common='')
    flow = Flow(name='parent', item_input=['x'], item_output=['z','extra'], sub_flows=[sf])
    compile_flow(flow)
"

# Probe 4: mixed contract — common satisfied, item missing.
run_subflow_probe \
  "mixed-item-missing" \
  "item_input field 'missing_item' not provided by SubFlow 'sf' contract" \
  "
    sf = SubFlow(name='sf', common_input=['uid'], item_input=['x'],
                 common_output=['greeting'], item_output=['z'])
    sf._add_op('transform_by_lua', common_input=['uid'], common_output=['greeting'],
               item_input=['missing_item'], item_output=['z'],
               lua_script='function f() return 0 end',
               function_for_item='f', function_for_common='')
    flow = Flow(name='parent', common_input=['uid'], item_input=['x','missing_item'],
                common_output=['greeting'], item_output=['z'], sub_flows=[sf])
    compile_flow(flow)
"

# Probe 5: mixed contract — item satisfied, common missing.
run_subflow_probe \
  "mixed-common-missing" \
  "common_input field 'missing_common' not provided by SubFlow 'sf' contract" \
  "
    sf = SubFlow(name='sf', common_input=['uid'], item_input=['x'],
                 common_output=['greeting'], item_output=['z'])
    sf._add_op('transform_by_lua', common_input=['missing_common'], common_output=['greeting'],
               item_input=['x'], item_output=['z'],
               lua_script='function f() return 0 end',
               function_for_item='f', function_for_common='')
    flow = Flow(name='parent', common_input=['uid','missing_common'], item_input=['x'],
                common_output=['greeting'], item_output=['z'], sub_flows=[sf])
    compile_flow(flow)
"

# Probe 6: template field referenced inside SubFlow whose contract doesn't
# include it (#74 + #78 cross — the SubFlow validator unions the three
# common_input buckets).
run_subflow_probe \
  "template-outside-subflow-contract" \
  "common_input field 'other_id' not provided by SubFlow 'sf' contract" \
  "
    sf = SubFlow(name='sf', common_input=['user_id'], item_input=['x'],
                 common_output=['greeting'], item_output=['z'])
    sf._add_op('synthetic_templated_op', common_input=[], common_output=['greeting'],
               item_input=[], item_output=[], key_template='hi {{other_id}}')
    sf._add_op('transform_by_lua', common_input=[], common_output=[],
               item_input=['x'], item_output=['z'],
               lua_script='function f() return 0 end',
               function_for_item='f', function_for_common='')
    flow = Flow(name='parent', common_input=['user_id','other_id'], item_input=['x'],
                common_output=['greeting'], item_output=['z'], sub_flows=[sf])
    compile_flow(flow)
"

# Probe 7: real common_input field inside an if branch still gated by the
# SubFlow contract — underscore filter only exempts skip-control fields.
run_subflow_probe \
  "if-branch-real-field-still-checked" \
  "common_input field 'secret' not provided by SubFlow 'sf' contract" \
  "
    sf = SubFlow(name='sf', common_input=['enabled'], item_input=['x'], item_output=['x'])
    sf.if_('{{enabled}} ~= nil')._add_op(
        'transform_by_lua', common_input=['secret'],
        item_input=['x'], item_output=['x'],
        lua_script='function f() return secret end',
        function_for_item='f', function_for_common='').end_if_()
    flow = Flow(name='parent', common_input=['enabled','secret'], item_input=['x'],
                item_output=['x'], sub_flows=[sf])
    compile_flow(flow)
"

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
