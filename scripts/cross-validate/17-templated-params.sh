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

# --- Runtime probe #2: float-source stringify (locks GoFormat parity) ---
#
# Pins the cross-runtime contract that a float-typed common field
# bound to a string-typed templatable param stringifies via Go
# fmt.Sprint semantics (5.0 -> "5"), not Java String.valueOf
# (5.0 -> "5.0") nor any toString variant. Same Redis server, fresh
# fixture.
FIXTURE_FLOAT="$REPO_ROOT/fixtures/pipelines/redis_templated_key_float.json"
if [[ -f "$FIXTURE_FLOAT" ]]; then
  WORK_TPL2="$WORK_DIR/templated_params_float"
  mkdir -p "$WORK_TPL2"
  CFG2="$WORK_TPL2/config.json"
  REQ2="$WORK_TPL2/request.json"
  EXPECTED_KEY2=$(python3 -c "
import json
with open('$FIXTURE_FLOAT') as f:
    data = json.load(f)
cfg = data['config']
case = data['cases'][0]
cfg_str = json.dumps(cfg).replace('PLACEHOLDER', '127.0.0.1:$REDIS_PORT')
with open('$CFG2', 'w') as cf:
    cf.write(cfg_str)
with open('$REQ2', 'w') as rf:
    json.dump(case['request'], rf)
print(case['expected_redis_key'])
")
  redis-cli -p $REDIS_PORT SET "$EXPECTED_KEY2" "hello_float" >/dev/null 2>&1

  go_out2=$("$WORK_DIR/pineapple-run" -config "$CFG2" -request "$REQ2" 2>/dev/null) || {
    fail "templated params float-source: Go engine failed"
    go_out2=""
  }
  java_out2=$(java_run page.liam.pine.RunCli -config "$CFG2" -request "$REQ2" 2>/dev/null) || {
    fail "templated params float-source: Java engine failed"
    java_out2=""
  }
  cpp_out2=""
  if [[ -n "${CPP_RUN:-}" ]]; then
    cpp_out2=$("$CPP_RUN" -config "$CFG2" -request "$REQ2" 2>/dev/null) || {
      fail "templated params float-source: C++ engine failed"
      cpp_out2=""
    }
  fi

  if [[ -n "$go_out2" && -n "$java_out2" ]]; then
    go_norm2=$(echo "$go_out2" | normalize_json)
    java_norm2=$(echo "$java_out2" | normalize_json)
    if [[ "$go_norm2" == "$java_norm2" ]]; then
      pass "templated params float-source parity Go vs Java (5.0 -> '5')"
    else
      fail "templated params float-source: Go vs Java divergence"
      diff <(echo "$go_norm2" | python3 -m json.tool) <(echo "$java_norm2" | python3 -m json.tool) >&2 || true
    fi
  fi
  if [[ -n "${CPP_RUN:-}" && -n "$go_out2" && -n "$cpp_out2" ]]; then
    go_norm2=$(echo "$go_out2" | normalize_json)
    cpp_norm2=$(echo "$cpp_out2" | normalize_json)
    if [[ "$go_norm2" == "$cpp_norm2" ]]; then
      pass "templated params float-source parity Go vs C++ (5.0 -> '5')"
    else
      fail "templated params float-source: Go vs C++ divergence"
      diff <(echo "$go_norm2" | python3 -m json.tool) <(echo "$cpp_norm2" | python3 -m json.tool) >&2 || true
    fi
  fi

  # Correctness: result/cache_hit prove the engines constructed
  # "5user1" (not "5.0user1") and hit the pre-populated key.
  if [[ -n "$go_out2" ]]; then
    got2=$(echo "$go_out2" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['common'].get('result',''))")
    got_hit2=$(echo "$go_out2" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['common'].get('cache_hit',''))")
    if [[ "$got2" == "hello_float" && "$got_hit2" == "True" ]]; then
      echo "    correctness verified: float source 5.0 stringified to '5' (key=$EXPECTED_KEY2)"
    else
      fail "templated params float-source: wrong result (got result='$got2', cache_hit='$got_hit2') — likely String.valueOf style stringify"
    fi
  fi
fi

# --- Runtime probe #3: SET-side templated key_prefix + ttl symmetry ---
#
# Symmetric counterpart to probe #1 (GET-side). The fixture wires a
# transform_redis_set with templated key_prefix={{tenant_id}} and
# templated ttl={{cache_ttl}}, then chains transform_redis_get to read
# back through the same templated key. Pins:
#   1. SET-side honors {{field}} resolution against the request frame
#      (regression net for redis_set.key_prefix templatable, T1).
#   2. int-typed templatable param resolution (regression net for
#      redis_set.ttl templatable, T2).
#   3. Cross-runtime: each engine's SET wrote to the same key, otherwise
#      the chained GET would miss and byte-exact compare would diverge.
FIXTURE_SET="$REPO_ROOT/fixtures/pipelines/redis_templated_set.json"
if [[ -f "$FIXTURE_SET" ]]; then
  redis-cli -p $REDIS_PORT FLUSHALL >/dev/null 2>&1
  WORK_TPL_SET="$WORK_DIR/templated_params_set"
  mkdir -p "$WORK_TPL_SET"
  CFG_SET="$WORK_TPL_SET/config.json"
  REQ_SET="$WORK_TPL_SET/request.json"
  EXPECTED_KEY_SET=$(python3 -c "
import json
with open('$FIXTURE_SET') as f:
    data = json.load(f)
cfg = data['config']
case = data['cases'][0]
cfg_str = json.dumps(cfg).replace('PLACEHOLDER', '127.0.0.1:$REDIS_PORT')
with open('$CFG_SET', 'w') as cf:
    cf.write(cfg_str)
with open('$REQ_SET', 'w') as rf:
    json.dump(case['request'], rf)
print(case['expected_redis_key'])
")

  # Verify the engine's SET landed at the templated key.
  # Decoupled from the chained-GET byte-exact compare below — if both
  # the SET and GET sides regressed in lock-step on one runtime (e.g. a
  # templating refactor missed both directions), the chained validation
  # alone could mask it. A direct redis-cli read after each engine pins
  # the SET-side contract independently of how that engine reads back.
  verify_set_landed() {
    local engine="$1"
    local got
    got=$(redis-cli -p $REDIS_PORT GET "$EXPECTED_KEY_SET" 2>/dev/null)
    if [[ "$got" == "hello_templated_set" ]]; then
      echo "    SET-side correctness verified: $engine wrote to templated key=$EXPECTED_KEY_SET"
    else
      fail "templated params SET-side: $engine did not write to templated key (got '$got' at key '$EXPECTED_KEY_SET')"
    fi
  }

  go_out_set=$("$WORK_DIR/pineapple-run" -config "$CFG_SET" -request "$REQ_SET" 2>/dev/null) || {
    fail "templated params SET-side: Go engine failed"
    go_out_set=""
  }
  [[ -n "$go_out_set" ]] && verify_set_landed "Go"

  redis-cli -p $REDIS_PORT FLUSHALL >/dev/null 2>&1
  java_out_set=$(java_run page.liam.pine.RunCli -config "$CFG_SET" -request "$REQ_SET" 2>/dev/null) || {
    fail "templated params SET-side: Java engine failed"
    java_out_set=""
  }
  [[ -n "$java_out_set" ]] && verify_set_landed "Java"

  cpp_out_set=""
  if [[ -n "${CPP_RUN:-}" ]]; then
    redis-cli -p $REDIS_PORT FLUSHALL >/dev/null 2>&1
    cpp_out_set=$("$CPP_RUN" -config "$CFG_SET" -request "$REQ_SET" 2>/dev/null) || {
      fail "templated params SET-side: C++ engine failed"
      cpp_out_set=""
    }
    [[ -n "$cpp_out_set" ]] && verify_set_landed "C++"
  fi

  if [[ -n "$go_out_set" && -n "$java_out_set" ]]; then
    go_norm_set=$(echo "$go_out_set" | normalize_json)
    java_norm_set=$(echo "$java_out_set" | normalize_json)
    if [[ "$go_norm_set" == "$java_norm_set" ]]; then
      pass "templated params SET-side parity Go vs Java (key_prefix + ttl)"
    else
      fail "templated params SET-side: Go vs Java divergence"
      diff <(echo "$go_norm_set" | python3 -m json.tool) <(echo "$java_norm_set" | python3 -m json.tool) >&2 || true
    fi
  fi
  if [[ -n "${CPP_RUN:-}" && -n "$go_out_set" && -n "$cpp_out_set" ]]; then
    go_norm_set=$(echo "$go_out_set" | normalize_json)
    cpp_norm_set=$(echo "$cpp_out_set" | normalize_json)
    if [[ "$go_norm_set" == "$cpp_norm_set" ]]; then
      pass "templated params SET-side parity Go vs C++ (key_prefix + ttl)"
    else
      fail "templated params SET-side: Go vs C++ divergence"
      diff <(echo "$go_norm_set" | python3 -m json.tool) <(echo "$cpp_norm_set" | python3 -m json.tool) >&2 || true
    fi
  fi
fi

# --- Error-path probes: byte-exact stderr across Go/Java/C++ ---
#
# The runtime resolver wording is already pinned at the unit-test layer
# (template_test.go / TemplateResolverTest.java / test_template.cpp).
# These probes lock the end-to-end contract: ExecutionError / ConfigError
# wrapping + scheduler path + pineapple-run CLI error surface must keep
# all three runtimes byte-for-byte identical, otherwise a wrapping
# refactor on one runtime can silently drift the user-facing error.
#
# Probe set:
#   #1 runtime missing-field — declared template source absent from request
#   #2 SKIPPED: int64 coerce-failure has no reachable end-to-end shape
#      (only string-typed templatable param exists in the operator set;
#      string coerce is identity). Wording pinned by unit tests.
#   #3 build-time non-bare-marker — hand-edited literal-surrounded marker
#
# We capture stderr only; stdout differs trivially across runtimes on
# failure paths (Go prints nothing; Java may print a trailing newline).
# Trailing newlines are stripped before compare.

probe_stderr() {
  # Run a runtime and print its stderr, trimming trailing whitespace so
  # diff(1) is byte-exact regardless of '\n' vs newline-suppressed.
  # SLF4J no-op binder warnings are stripped — they're known boot noise
  # that varies by classpath state, not a pine error contract.
  local runtime="$1"; shift
  local cfg="$1"; shift
  local req="$1"; shift
  case "$runtime" in
    go)
      "$WORK_DIR/pineapple-run" -config "$cfg" -request "$req" 2>&1 >/dev/null || true
      ;;
    java)
      java_run page.liam.pine.RunCli -config "$cfg" -request "$req" 2>&1 >/dev/null | grep -v '^SLF4J:' || true
      ;;
    cpp)
      "$CPP_RUN" -config "$cfg" -request "$req" 2>&1 >/dev/null || true
      ;;
  esac
}

probe_compare() {
  local label="$1"
  local expected="$2"
  local go_out="$3"
  local java_out="$4"
  local cpp_out="${5:-}"
  if [[ "$go_out" != "$expected" ]]; then
    fail "templated error-path $label: Go drift"
    printf '    expected: %q\n    got:      %q\n' "$expected" "$go_out" >&2
    return
  fi
  if [[ "$java_out" != "$expected" ]]; then
    fail "templated error-path $label: Java drift"
    printf '    expected: %q\n    got:      %q\n' "$expected" "$java_out" >&2
    return
  fi
  if [[ -n "${CPP_RUN:-}" && "$cpp_out" != "$expected" ]]; then
    fail "templated error-path $label: C++ drift"
    printf '    expected: %q\n    got:      %q\n' "$expected" "$cpp_out" >&2
    return
  fi
  if [[ -n "${CPP_RUN:-}" ]]; then
    pass "templated error-path $label byte-exact across Go/Java/C++"
  else
    pass "templated error-path $label byte-exact across Go/Java (C++ skipped)"
  fi
}

# Probe #1: runtime missing-field.
# Config declares tenant_id as template source; request omits it.
# Engine builds fine, frame.common("tenant_id") is missing at execute,
# resolver throws ExecutionError, CLI prints:
#   execution error: pine: execution error in operator "get_cache":
#     templated param "key_prefix" references common field "tenant_id" which is missing
WORK_TPL3="$WORK_DIR/templated_params_missing"
mkdir -p "$WORK_TPL3"
CFG3="$WORK_TPL3/config.json"
REQ3="$WORK_TPL3/request.json"
python3 -c "
import json
cfg = {
  'resource_config': {
    'redis_conn': {'type': 'redis_connection', 'interval': -1,
                   'params': {'addr': '127.0.0.1:$REDIS_PORT'}}
  },
  'pipeline_config': {
    'operators': {
      'get_cache': {
        'type_name': 'transform_redis_get',
        'resource_name': 'redis_conn',
        'key_prefix': '{{tenant_id}}',
        'fail_on_error': True,
        '\$metadata': {
          'common_input': ['uid'],
          'common_input_template': ['tenant_id'],
          'common_output': ['result', 'cache_hit'],
          'item_input': [], 'item_output': []
        }
      }
    }
  },
  'pipeline_group': {'main': {'pipeline': ['get_cache']}},
  'flow_contract': {
    'common_input': ['uid'], 'item_input': [],
    'common_output': ['uid', 'result', 'cache_hit'], 'item_output': []
  }
}
with open('$CFG3', 'w') as f: json.dump(cfg, f)
with open('$REQ3', 'w') as f: json.dump({'common': {'uid': 'user1'}, 'items': []}, f)
"

EXP1='execution error: pine: execution error in operator "get_cache": templated param "key_prefix" references common field "tenant_id" which is missing'
go1=$(probe_stderr go   "$CFG3" "$REQ3"); go1="${go1%$'\n'}"
ja1=$(probe_stderr java "$CFG3" "$REQ3"); ja1="${ja1%$'\n'}"
cp1=""
if [[ -n "${CPP_RUN:-}" ]]; then
  cp1=$(probe_stderr cpp "$CFG3" "$REQ3"); cp1="${cp1%$'\n'}"
fi
probe_compare "missing-field" "$EXP1" "$go1" "$ja1" "$cp1"

# Probe #3: build-time non-bare-marker.
# Hand-edited `tenant:{{tenant_id}}` violates the L0 contract; engine
# build rejects via ConfigError. No Redis touched. CLI prints:
#   error creating engine: pine: config error: operator "get_cache":
#     param "key_prefix" value "tenant:{{tenant_id}}" must be a bare {{field}} marker
WORK_TPL4="$WORK_DIR/templated_params_nonbare"
mkdir -p "$WORK_TPL4"
CFG4="$WORK_TPL4/config.json"
REQ4="$WORK_TPL4/request.json"
python3 -c "
import json
cfg = {
  'resource_config': {
    'redis_conn': {'type': 'redis_connection', 'interval': -1,
                   'params': {'addr': '127.0.0.1:$REDIS_PORT'}}
  },
  'pipeline_config': {
    'operators': {
      'get_cache': {
        'type_name': 'transform_redis_get',
        'resource_name': 'redis_conn',
        'key_prefix': 'tenant:{{tenant_id}}',
        'fail_on_error': True,
        '\$metadata': {
          'common_input': ['uid'],
          'common_input_template': ['tenant_id'],
          'common_output': ['result', 'cache_hit'],
          'item_input': [], 'item_output': []
        }
      }
    }
  },
  'pipeline_group': {'main': {'pipeline': ['get_cache']}},
  'flow_contract': {
    'common_input': ['tenant_id', 'uid'], 'item_input': [],
    'common_output': ['tenant_id', 'uid', 'result', 'cache_hit'], 'item_output': []
  }
}
with open('$CFG4', 'w') as f: json.dump(cfg, f)
with open('$REQ4', 'w') as f: json.dump({'common': {'tenant_id': 'acme', 'uid': 'user1'}, 'items': []}, f)
"

EXP3='error creating engine: pine: config error: operator "get_cache": param "key_prefix" value "tenant:{{tenant_id}}" must be a bare {{field}} marker'
go3=$(probe_stderr go   "$CFG4" "$REQ4"); go3="${go3%$'\n'}"
ja3=$(probe_stderr java "$CFG4" "$REQ4"); ja3="${ja3%$'\n'}"
cp3=""
if [[ -n "${CPP_RUN:-}" ]]; then
  cp3=$(probe_stderr cpp "$CFG4" "$REQ4"); cp3="${cp3%$'\n'}"
fi
probe_compare "non-bare-marker" "$EXP3" "$go3" "$ja3" "$cp3"

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
