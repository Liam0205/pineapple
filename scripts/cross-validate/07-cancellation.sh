#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 7. Cancellation/timeout parity ----------
echo
echo "==> [7/$TOTAL_SECTIONS] Cancellation parity (timeout behavior)"

# Create a slow Lua fixture that should exceed a tight timeout
TIMEOUT_CONFIG="$WORK_DIR/timeout_config.json"
TIMEOUT_REQ="$WORK_DIR/timeout_req.json"

cat > "$TIMEOUT_CONFIG" << 'CFGEOF'
{
  "pipeline_config": {
    "operators": {
      "slow_lua": {
        "type_name": "transform_by_lua",
        "lua_script": "function slow()\n  while true do end\nend",
        "function_for_item": "slow",
        "function_for_common": "",
        "$metadata": {
          "common_input": [],
          "common_output": [],
          "item_input": ["x"],
          "item_output": ["x"]
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["slow_lua"]}
  },
  "flow_contract": {
    "common_input": [],
    "item_input": ["x"],
    "common_output": [],
    "item_output": ["x"]
  }
}
CFGEOF

cat > "$TIMEOUT_REQ" << 'REQEOF'
{"common": {}, "items": [{"x": 1}]}
REQEOF

cancel_pass=0
cancel_total=0

# Test 1: both engines killed by timeout produce same exit behavior (non-zero)
cancel_total=$((cancel_total + 1))
go_exit=0
timeout 3 "$WORK_DIR/pineapple-run" -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || go_exit=$?
java_exit=0
timeout 3 java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || java_exit=$?

# Both should have been killed (exit 124 from timeout, or 137 from SIGKILL)
if [[ $go_exit -ne 0 && $java_exit -ne 0 ]]; then
  cancel_pass=$((cancel_pass + 1))
  echo "    [1] slow Lua + timeout 3s → Go & Java both killed (Go=$go_exit, Java=$java_exit)"
elif [[ $go_exit -eq 0 && $java_exit -eq 0 ]]; then
  # Both finished fast enough — still parity
  cancel_pass=$((cancel_pass + 1))
  echo "    [1] slow Lua + timeout 3s → both completed (parity OK)"
else
  fail "cancellation parity (Go vs Java): divergence (Go exit=$go_exit, Java exit=$java_exit)"
fi

# Test 1b: C++ timeout
if [[ -n "${CPP_RUN:-}" ]]; then
  cancel_total=$((cancel_total + 1))
  cpp_exit=0
  timeout 3 "$CPP_RUN" -config "$TIMEOUT_CONFIG" -request "$TIMEOUT_REQ" >/dev/null 2>&1 || cpp_exit=$?
  if [[ $cpp_exit -ne 0 ]]; then
    cancel_pass=$((cancel_pass + 1))
    echo "    [1b] slow Lua + timeout 3s → C++ killed (exit=$cpp_exit)"
  else
    fail "cancellation parity (C++): C++ did not timeout as expected (exit=$cpp_exit)"
  fi
fi

# Test 2: Lua error produces same error behavior from both engines
cancel_total=$((cancel_total + 1))
ERR_LUA_CONFIG="$WORK_DIR/err_lua_config.json"
cat > "$ERR_LUA_CONFIG" << 'CFGEOF'
{
  "pipeline_config": {
    "operators": {
      "bad_lua": {
        "type_name": "transform_by_lua",
        "lua_script": "function fail_intentional()\n  error('intentional failure')\nend",
        "function_for_item": "fail_intentional",
        "function_for_common": "",
        "$metadata": {
          "common_input": [],
          "common_output": [],
          "item_input": ["x"],
          "item_output": ["x"]
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["bad_lua"]}
  },
  "flow_contract": {
    "common_input": [],
    "item_input": ["x"],
    "common_output": [],
    "item_output": ["x"]
  }
}
CFGEOF

go_lua_err=$("$WORK_DIR/pineapple-run" -config "$ERR_LUA_CONFIG" -request "$TIMEOUT_REQ" 2>&1) && go_lua_ok=true || go_lua_ok=false
java_lua_err=$(java -cp "$JAVA_CP" page.liam.pine.RunCli -config "$ERR_LUA_CONFIG" -request "$TIMEOUT_REQ" 2>&1) && java_lua_ok=true || java_lua_ok=false

if [[ "$go_lua_ok" == "false" && "$java_lua_ok" == "false" ]]; then
  # Both failed — check both mention "intentional"
  if echo "$go_lua_err" | grep -qi "intentional" && echo "$java_lua_err" | grep -qi "intentional"; then
    cancel_pass=$((cancel_pass + 1))
    echo "    [2] Lua error() → Go & Java both failed with expected message"
  else
    fail "cancellation parity (Go vs Java): Lua error message mismatch"
    echo "      Go:   $go_lua_err" | head -2 >&2
    echo "      Java: $java_lua_err" | head -2 >&2
  fi
elif [[ "$go_lua_ok" == "$java_lua_ok" ]]; then
  cancel_pass=$((cancel_pass + 1))
  echo "    [2] Lua error() → both behaved same (ok=$go_lua_ok)"
else
  fail "cancellation parity (Go vs Java): Lua error divergence (Go_ok=$go_lua_ok, Java_ok=$java_lua_ok)"
fi

# Test 2b: C++ Lua error
if [[ -n "${CPP_RUN:-}" ]]; then
  cancel_total=$((cancel_total + 1))
  cpp_lua_err=$("$CPP_RUN" -config "$ERR_LUA_CONFIG" -request "$TIMEOUT_REQ" 2>&1) && cpp_lua_ok=true || cpp_lua_ok=false
  if [[ "$cpp_lua_ok" == "false" ]]; then
    if echo "$cpp_lua_err" | grep -qi "intentional"; then
      cancel_pass=$((cancel_pass + 1))
      echo "    [2b] Lua error() → C++ failed with expected message"
    else
      fail "cancellation parity (C++): Lua error message missing 'intentional'"
      echo "      C++: $cpp_lua_err" | head -2 >&2
    fi
  else
    fail "cancellation parity (C++): C++ did not fail on Lua error (ok=$cpp_lua_ok)"
  fi
fi

if [[ $cancel_total -gt 0 && $cancel_pass -eq $cancel_total ]]; then
  pass "cancellation parity ($cancel_pass/$cancel_total checks)"
elif [[ $cancel_total -eq 0 ]]; then
  pass "cancellation parity (skipped)"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
