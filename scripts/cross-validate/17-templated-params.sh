#!/usr/bin/env bash
# Section 17: Templated params byte-exact parity across 4 runtimes (issue #74)
#
# Verifies that `{{field}}` interpolation in operator params produces identical
# behavior across Apple DSL (compile-time auto-injection), pine-go, pine-java,
# and pine-cpp (runtime resolution). The canonical consumer is
# transform_redis_get.key_prefix.
#
# Apple part: compile a tiny Flow whose key_prefix references {{tenant_id}}
#   and verify the compiler auto-injects tenant_id into the op's common_input
#   (issue #25 path) and preserves the {{tenant_id}} marker in the rendered
#   config.
# Runtime part: pre-populate a Redis key, run the fixture through Go/Java/C++,
#   byte-exact compare each response, and confirm the actual Redis key under
#   which the data lives matches the templated construction.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

echo
echo "==> [17/$TOTAL_SECTIONS] Templated params parity (Apple compile + Go/Java/C++ runtime)"

FIXTURE="$REPO_ROOT/fixtures/pipelines/redis_templated_key.json"
if [[ ! -f "$FIXTURE" ]]; then
  fail "templated params: fixture missing at $FIXTURE"
  return 0
fi

# --- Apple part: auto-injection of templated common fields (issue #25) ---
if python3 -c "
import sys
sys.path.insert(0, '$REPO_ROOT')
from apple.flow import Flow
from apple.compiler import compile_flow
from apple_generated.resources import RedisConnectionResource

flow = Flow(name='templated_get',
            common_input=['tenant_id', 'uid'],
            common_output=['result', 'cache_hit'])
flow.resource('redis_conn', RedisConnectionResource(addr='127.0.0.1:6379'))
# Deliberately omit tenant_id from the op's common_input; the compiler must
# auto-inject it because key_prefix references {{tenant_id}}.
flow.transform_redis_get(
    resource_name='redis_conn',
    key_prefix='{{tenant_id}}',
    common_input=['uid'],
    common_output=['result', 'cache_hit'],
)
cfg = compile_flow(flow)
op = list(cfg['pipeline_config']['operators'].values())[0]
meta = op['\$metadata']
ci = meta['common_input']
cit = meta.get('common_input_template', [])
# Post-#74 bucket layout: tenant_id is auto-injected into the dedicated
# template bucket (kept out of operator-visible input); uid stays in
# the business common_input.
assert 'tenant_id' in cit, f'tenant_id not in common_input_template: {cit}'
assert 'tenant_id' not in ci, f'tenant_id leaked into common_input: {ci}'
assert 'uid' in ci, f'uid missing: {ci}'
assert op['key_prefix'] == '{{tenant_id}}', f'marker stripped: {op[\"key_prefix\"]}'
" 2>&1; then
  pass "Apple DSL: templated key_prefix routes {{tenant_id}} into common_input_template (#74)"
else
  fail "Apple DSL: templated param 3-bucket routing broken"
fi

# --- Runtime part: byte-exact /execute output across Go/Java/C++ ---

if ! which redis-server >/dev/null 2>&1; then
  pass "templated params runtime parity (skipped: redis-server not found)"
  return 0
fi

REDIS_PORT=21017
redis-server --port $REDIS_PORT --daemonize yes --logfile /dev/null --save "" --appendonly no

redis_ready=false
for i in $(seq 1 20); do
  if redis-cli -p $REDIS_PORT PING 2>/dev/null | grep -q PONG; then
    redis_ready=true
    break
  fi
  sleep 0.1
done

if [[ "$redis_ready" != "true" ]]; then
  fail "templated params: redis-server failed to start on port $REDIS_PORT"
  return 0
fi
echo "    Redis ready on port $REDIS_PORT"

WORK_TPL="$WORK_DIR/templated_params"
mkdir -p "$WORK_TPL"
CFG="$WORK_TPL/config.json"
REQ="$WORK_TPL/request.json"
EXPECTED_KEY=$(python3 -c "
import json
with open('$FIXTURE') as f:
    data = json.load(f)
cfg = data['config']
case = data['cases'][0]
# Inject the live Redis address.
cfg_str = json.dumps(cfg).replace('PLACEHOLDER', '127.0.0.1:$REDIS_PORT')
with open('$CFG', 'w') as cf:
    cf.write(cfg_str)
with open('$REQ', 'w') as rf:
    json.dump(case['request'], rf)
print(case['expected_redis_key'])
")

# Pre-populate Redis with the value the get should retrieve. The key here is
# what the engines MUST construct from the templated prefix + uid suffix.
redis-cli -p $REDIS_PORT SET "$EXPECTED_KEY" "hello_templated" >/dev/null 2>&1

# Go engine
go_out=$("$WORK_DIR/pineapple-run" -config "$CFG" -request "$REQ" 2>/dev/null) || {
  fail "templated params: Go engine failed"
  go_out=""
}
# Java engine
java_out=$(java_run page.liam.pine.RunCli -config "$CFG" -request "$REQ" 2>/dev/null) || {
  fail "templated params: Java engine failed"
  java_out=""
}
# C++ engine (optional)
cpp_out=""
if [[ -n "${CPP_RUN:-}" ]]; then
  cpp_out=$("$CPP_RUN" -config "$CFG" -request "$REQ" 2>/dev/null) || {
    fail "templated params: C++ engine failed"
    cpp_out=""
  }
fi

# Byte-exact compare (after JSON normalization to fold numeric type / key
# order differences, matching the convention used by other sections).
tpl_pass=0
tpl_total=0
cpp_tpl_pass=0
cpp_tpl_total=0

if [[ -n "$go_out" && -n "$java_out" ]]; then
  tpl_total=$((tpl_total + 1))
  go_norm=$(echo "$go_out" | normalize_json)
  java_norm=$(echo "$java_out" | normalize_json)
  if [[ "$go_norm" == "$java_norm" ]]; then
    tpl_pass=$((tpl_pass + 1))
    echo "    templated key_prefix parity Go vs Java → match"
  else
    fail "templated params: Go vs Java divergence"
    diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$java_norm" | python3 -m json.tool) >&2 || true
  fi
fi

if [[ -n "${CPP_RUN:-}" && -n "$go_out" && -n "$cpp_out" ]]; then
  cpp_tpl_total=$((cpp_tpl_total + 1))
  go_norm=$(echo "$go_out" | normalize_json)
  cpp_norm=$(echo "$cpp_out" | normalize_json)
  if [[ "$go_norm" == "$cpp_norm" ]]; then
    cpp_tpl_pass=$((cpp_tpl_pass + 1))
    echo "    templated key_prefix parity Go vs C++ → match"
  else
    fail "templated params: Go vs C++ divergence"
    diff <(echo "$go_norm" | python3 -m json.tool) <(echo "$cpp_norm" | python3 -m json.tool) >&2 || true
  fi
fi

# Correctness: the engines must have consulted the templated key. We verify
# that by inspecting the response payload — the pre-populated value flowing
# back as `result` proves the constructed key matched EXPECTED_KEY.
if [[ -n "$go_out" ]]; then
  got=$(echo "$go_out" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['common'].get('result',''))")
  got_hit=$(echo "$go_out" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['common'].get('cache_hit',''))")
  if [[ "$got" == "hello_templated" && "$got_hit" == "True" ]]; then
    echo "    correctness verified: result='hello_templated', cache_hit=True (key=$EXPECTED_KEY)"
  else
    fail "templated params: wrong result (got result='$got', cache_hit='$got_hit')"
  fi
fi

redis-cli -p $REDIS_PORT SHUTDOWN NOSAVE >/dev/null 2>&1 || true

if [[ $tpl_total -gt 0 && $tpl_pass -eq $tpl_total ]]; then
  pass "templated params runtime parity Go vs Java ($tpl_pass/$tpl_total)"
elif [[ $tpl_total -eq 0 ]]; then
  pass "templated params runtime parity Go vs Java (skipped: setup failed)"
fi
if [[ -n "${CPP_RUN:-}" ]]; then
  if [[ $cpp_tpl_total -gt 0 && $cpp_tpl_pass -eq $cpp_tpl_total ]]; then
    pass "templated params runtime parity Go vs C++ ($cpp_tpl_pass/$cpp_tpl_total)"
  elif [[ $cpp_tpl_total -eq 0 ]]; then
    pass "templated params runtime parity Go vs C++ (skipped: setup failed)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
