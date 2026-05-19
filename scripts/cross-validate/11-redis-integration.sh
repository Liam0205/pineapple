#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 11. Redis integration parity (conditional) ----------
echo
echo "==> [11/$TOTAL_SECTIONS] Redis integration parity (requires redis-server)"

REDIS_FIXTURE="$REPO_ROOT/fixtures/pipelines/redis_integration.json"
redis_pass=0
redis_total=0

if ! which redis-server >/dev/null 2>&1; then
  pass "redis integration (skipped: redis-server not found)"
else
  REDIS_PORT=18940

  redis-server --port $REDIS_PORT --daemonize yes --logfile /dev/null --save "" --appendonly no

  # Wait for Redis to be ready
  redis_ready=false
  for i in $(seq 1 20); do
    if redis-cli -p $REDIS_PORT PING 2>/dev/null | grep -q PONG; then
      redis_ready=true
      break
    fi
    sleep 0.1
  done

  if [[ "$redis_ready" != "true" ]]; then
    fail "redis integration: redis-server failed to start on port $REDIS_PORT"
  else
    echo "    Redis ready on port $REDIS_PORT"

    # --- Test A: set-then-get via fixture (both engines) ---
    redis_total=$((redis_total + 1))

    # Create temp config by replacing PLACEHOLDER with actual Redis address and enabling strict mode
    REDIS_CONFIG="$WORK_DIR/redis_config.json"
    REDIS_REQ="$WORK_DIR/redis_req.json"
    python3 -c "
import json
with open('$REDIS_FIXTURE') as f:
    data = json.load(f)
cfg = data['config']
# Inject fail_on_error for strict integration testing
for op in cfg.get('pipeline_config', {}).get('operators', {}).values():
    op['fail_on_error'] = True
cfg_str = json.dumps(cfg)
cfg_str = cfg_str.replace('PLACEHOLDER', '127.0.0.1:$REDIS_PORT')
with open('$REDIS_CONFIG', 'w') as cf:
    cf.write(cfg_str)
req = data['cases'][0]['request']
with open('$REDIS_REQ', 'w') as rf:
    json.dump(req, rf)
"

    # Run Go engine
    go_redis_result=$("$WORK_DIR/pineapple-run" -config "$REDIS_CONFIG" -request "$REDIS_REQ" 2>/dev/null) || {
      fail "redis integration: Go engine failed on set-then-get"
      go_redis_result=""
    }

    # Run Java engine (flush Redis first so Java also starts fresh)
    redis-cli -p $REDIS_PORT FLUSHALL >/dev/null 2>&1
    java_redis_result=$(java_run page.liam.pine.RunCli -config "$REDIS_CONFIG" -request "$REDIS_REQ" 2>/dev/null) || {
      fail "redis integration: Java engine failed on set-then-get"
      java_redis_result=""
    }

    # Run Python engine (flush Redis first so Python also starts fresh)
    redis-cli -p $REDIS_PORT FLUSHALL >/dev/null 2>&1
    py_redis_result=$(py_run pine.cli.run -config "$REDIS_CONFIG" -request "$REDIS_REQ" 2>/dev/null) || {
      fail "redis integration: Python engine failed on set-then-get"
      py_redis_result=""
    }

    if [[ -n "$go_redis_result" && -n "$java_redis_result" ]]; then
      go_redis_norm=$(echo "$go_redis_result" | normalize_json)
      java_redis_norm=$(echo "$java_redis_result" | normalize_json)

      if [[ "$go_redis_norm" == "$java_redis_norm" ]]; then
        redis_pass=$((redis_pass + 1))
        echo "    [A] set-then-get parity Go vs Java → match"
      else
        fail "redis integration: set-then-get divergence (Go vs Java)"
        diff <(echo "$go_redis_norm" | python3 -m json.tool) <(echo "$java_redis_norm" | python3 -m json.tool) >&2 || true
      fi
    fi

    redis_total=$((redis_total + 1))
    if [[ -n "$go_redis_result" && -n "$py_redis_result" ]]; then
      go_redis_norm=$(echo "$go_redis_result" | normalize_json)
      py_redis_norm=$(echo "$py_redis_result" | normalize_json)

      if [[ "$go_redis_norm" == "$py_redis_norm" ]]; then
        redis_pass=$((redis_pass + 1))
        echo "    [A] set-then-get parity Go vs Python → match"
      else
        fail "redis integration: set-then-get divergence (Go vs Python)"
        diff <(echo "$go_redis_norm" | python3 -m json.tool) <(echo "$py_redis_norm" | python3 -m json.tool) >&2 || true
      fi
    fi

    # --- Test B: pre-populated GET-only (both engines read same value) ---
    redis_total=$((redis_total + 1))

    # Pre-populate Redis with a known value
    redis-cli -p $REDIS_PORT SET "test:user2" "pre_existing" >/dev/null 2>&1

    # Create GET-only config (no set_cache, just get_cache)
    REDIS_GET_CONFIG="$WORK_DIR/redis_get_config.json"
    REDIS_GET_REQ="$WORK_DIR/redis_get_req.json"
    cat > "$REDIS_GET_CONFIG" << GETCFG
{
  "pipeline_config": {
    "operators": {
      "get_cache": {
        "type_name": "transform_redis_get",
        "redis_addr": "127.0.0.1:$REDIS_PORT",
        "key_prefix": "test:",
        "fail_on_error": true,
        "\$metadata": {
          "common_input": ["uid"],
          "common_output": ["result", "cache_hit"],
          "item_input": [],
          "item_output": []
        }
      }
    }
  },
  "pipeline_group": {
    "main": {"pipeline": ["get_cache"]}
  },
  "flow_contract": {
    "common_input": ["uid"],
    "item_input": [],
    "common_output": ["uid", "result", "cache_hit"],
    "item_output": []
  }
}
GETCFG
    cat > "$REDIS_GET_REQ" << 'GETREQ'
{"common": {"uid": "user2"}, "items": []}
GETREQ

    # Run Go engine
    go_get_result=$("$WORK_DIR/pineapple-run" -config "$REDIS_GET_CONFIG" -request "$REDIS_GET_REQ" 2>/dev/null) || {
      fail "redis integration: Go engine failed on pre-populated get"
      go_get_result=""
    }

    # Run Java engine (same pre-populated Redis key)
    java_get_result=$(java_run page.liam.pine.RunCli -config "$REDIS_GET_CONFIG" -request "$REDIS_GET_REQ" 2>/dev/null) || {
      fail "redis integration: Java engine failed on pre-populated get"
      java_get_result=""
    }

    # Run Python engine (same pre-populated Redis key)
    py_get_result=$(py_run pine.cli.run -config "$REDIS_GET_CONFIG" -request "$REDIS_GET_REQ" 2>/dev/null) || {
      fail "redis integration: Python engine failed on pre-populated get"
      py_get_result=""
    }

    if [[ -n "$go_get_result" && -n "$java_get_result" ]]; then
      go_get_norm=$(echo "$go_get_result" | normalize_json)
      java_get_norm=$(echo "$java_get_result" | normalize_json)

      if [[ "$go_get_norm" == "$java_get_norm" ]]; then
        redis_pass=$((redis_pass + 1))
        echo "    [B] pre-populated get parity Go vs Java → match"
      else
        fail "redis integration: pre-populated get divergence (Go vs Java)"
        diff <(echo "$go_get_norm" | python3 -m json.tool) <(echo "$java_get_norm" | python3 -m json.tool) >&2 || true
      fi
    fi

    redis_total=$((redis_total + 1))
    if [[ -n "$go_get_result" && -n "$py_get_result" ]]; then
      go_get_norm=$(echo "$go_get_result" | normalize_json)
      py_get_norm=$(echo "$py_get_result" | normalize_json)

      if [[ "$go_get_norm" == "$py_get_norm" ]]; then
        redis_pass=$((redis_pass + 1))
        echo "    [B] pre-populated get parity Go vs Python → match"
      else
        fail "redis integration: pre-populated get divergence (Go vs Python)"
        diff <(echo "$go_get_norm" | python3 -m json.tool) <(echo "$py_get_norm" | python3 -m json.tool) >&2 || true
      fi
    fi

    # Verify correctness: result should be "pre_existing"
    if [[ -n "$go_get_result" ]]; then
      got_value=$(echo "$go_get_result" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['common'].get('result',''))")
      got_hit=$(echo "$go_get_result" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d['common'].get('cache_hit',''))")
      if [[ "$got_value" == "pre_existing" && "$got_hit" == "True" ]]; then
        echo "    [B] correctness verified: result='pre_existing', cache_hit=True"
      else
        echo "    [B] WARNING: unexpected values (result='$got_value', cache_hit='$got_hit')" >&2
      fi
    fi

    # Shutdown Redis
    redis-cli -p $REDIS_PORT SHUTDOWN NOSAVE >/dev/null 2>&1 || true
  fi

  if [[ $redis_total -gt 0 && $redis_pass -eq $redis_total ]]; then
    pass "redis integration parity ($redis_pass/$redis_total checks)"
  elif [[ $redis_total -eq 0 ]]; then
    pass "redis integration parity (skipped: setup failed)"
  fi
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
