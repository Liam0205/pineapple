#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 8. Concurrent request parity ----------
echo
echo "==> [8/$TOTAL_SECTIONS] Concurrent request parity"

# Use the first server pair config (transform_then_filter)
SRV_FIXTURE="$REPO_ROOT/fixtures/pipelines/transform_then_filter.json"
CONC_CONFIG="$WORK_DIR/conc_config.json"
python3 -c "
import json
with open('$SRV_FIXTURE') as f:
    data = json.load(f)
with open('$CONC_CONFIG', 'w') as cf:
    json.dump(data.get('config', {}), cf)
"

CONC_REQ=$(python3 -c "
import json
with open('$SRV_FIXTURE') as f:
    data = json.load(f)
print(json.dumps(data['cases'][0]['request']))
")

GO_CONC_PORT=18920
JAVA_CONC_PORT=18921

"$WORK_DIR/pineapple-server" -config "$CONC_CONFIG" -addr ":$GO_CONC_PORT" &
GO_SRV_PID=$!
java -cp "$JAVA_CP" -Dpine.config="$CONC_CONFIG" -Dpine.port=$JAVA_CONC_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

conc_pass=0
conc_total=0

if srv_ready $GO_CONC_PORT && srv_ready $JAVA_CONC_PORT; then
  echo "    Servers ready. Sending 10 concurrent requests to each..."

  # Send 10 concurrent requests to Go
  curl_pids=()
  for i in $(seq 1 10); do
    curl -s -X POST -H "Content-Type: application/json" -d "$CONC_REQ" "http://localhost:$GO_CONC_PORT/execute" > "$WORK_DIR/conc_go_$i.json" &
    curl_pids+=($!)
  done
  wait "${curl_pids[@]}"

  # Send 10 concurrent requests to Java
  curl_pids=()
  for i in $(seq 1 10); do
    curl -s -X POST -H "Content-Type: application/json" -d "$CONC_REQ" "http://localhost:$JAVA_CONC_PORT/execute" > "$WORK_DIR/conc_java_$i.json" &
    curl_pids+=($!)
  done
  wait "${curl_pids[@]}"

  # All Go results should be identical to each other
  conc_total=$((conc_total + 1))
  go_first=$(cat "$WORK_DIR/conc_go_1.json" | normalize_json)
  go_all_match=true
  for i in $(seq 2 10); do
    go_i=$(cat "$WORK_DIR/conc_go_$i.json" | normalize_json)
    if [[ "$go_first" != "$go_i" ]]; then
      go_all_match=false
      break
    fi
  done

  java_first=$(cat "$WORK_DIR/conc_java_1.json" | normalize_json)
  java_all_match=true
  for i in $(seq 2 10); do
    java_i=$(cat "$WORK_DIR/conc_java_$i.json" | normalize_json)
    if [[ "$java_first" != "$java_i" ]]; then
      java_all_match=false
      break
    fi
  done

  if [[ "$go_all_match" == "true" && "$java_all_match" == "true" ]]; then
    conc_pass=$((conc_pass + 1))
    echo "    [1] All 10 concurrent responses identical within each engine"
  else
    fail "concurrent: responses differ within engine (Go_consistent=$go_all_match, Java_consistent=$java_all_match)"
  fi

  # Go and Java results should match each other
  conc_total=$((conc_total + 1))
  if [[ "$go_first" == "$java_first" ]]; then
    conc_pass=$((conc_pass + 1))
    echo "    [2] Go vs Java concurrent response → match"
  else
    fail "concurrent: Go vs Java divergence under concurrent load"
    diff <(echo "$go_first" | python3 -m json.tool) <(echo "$java_first" | python3 -m json.tool) >&2 || true
  fi

  # Check stats consistency: exec_count should be 10 for each operator
  conc_total=$((conc_total + 1))
  go_exec_counts=$(curl -s "http://localhost:$GO_CONC_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ops = d.get('operators', {})
counts = [v.get('exec_count', 0) for v in ops.values()]
print(min(counts), max(counts))
")
  java_exec_counts=$(curl -s "http://localhost:$JAVA_CONC_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ops = d.get('operators', {})
counts = [v.get('exec_count', 0) for v in ops.values()]
print(min(counts), max(counts))
")

  if [[ "$go_exec_counts" == "10 10" && "$java_exec_counts" == "10 10" ]]; then
    conc_pass=$((conc_pass + 1))
    echo "    [3] Stats exec_count = 10 for all operators (both engines)"
  else
    fail "concurrent: stats exec_count mismatch (Go=$go_exec_counts, Java=$java_exec_counts)"
  fi

  kill $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
else
  fail "concurrent: servers failed to start"
  kill $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
fi

if [[ $conc_total -gt 0 && $conc_pass -eq $conc_total ]]; then
  pass "concurrent request parity ($conc_pass/$conc_total checks)"
elif [[ $conc_total -eq 0 ]]; then
  pass "concurrent request parity (skipped)"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
