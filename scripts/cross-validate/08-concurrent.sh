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

GO_CONC_PORT=19001
JAVA_CONC_PORT=19002
PY_CONC_PORT=19003
CPP_CONC_PORT=19004

"$WORK_DIR/pineapple-server" -config "$CONC_CONFIG" -addr ":$GO_CONC_PORT" &
GO_SRV_PID=$!
java -cp "$JAVA_CP" -Dpine.config="$CONC_CONFIG" -Dpine.port=$JAVA_CONC_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!
(cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.server -config "$CONC_CONFIG" -addr ":$PY_CONC_PORT") &
PY_SRV_PID=$!
CPP_SRV_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$CONC_CONFIG" -addr ":$CPP_CONC_PORT" &
  CPP_SRV_PID=$!
fi

conc_pass=0
conc_total=0
cpp_conc_pass=0
cpp_conc_total=0
cpp_srv_ready=false

if srv_ready $GO_CONC_PORT && srv_ready $JAVA_CONC_PORT && srv_ready $PY_CONC_PORT; then
  if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $CPP_CONC_PORT; then
    cpp_srv_ready=true
  fi
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

  # Send 10 concurrent requests to Python
  curl_pids=()
  for i in $(seq 1 10); do
    curl -s -X POST -H "Content-Type: application/json" -d "$CONC_REQ" "http://localhost:$PY_CONC_PORT/execute" > "$WORK_DIR/conc_py_$i.json" &
    curl_pids+=($!)
  done
  wait "${curl_pids[@]}"

  # Send 10 concurrent requests to C++
  if $cpp_srv_ready; then
    curl_pids=()
    for i in $(seq 1 10); do
      curl -s -X POST -H "Content-Type: application/json" -d "$CONC_REQ" "http://localhost:$CPP_CONC_PORT/execute" > "$WORK_DIR/conc_cpp_$i.json" &
      curl_pids+=($!)
    done
    wait "${curl_pids[@]}"
  fi

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

  py_first=$(cat "$WORK_DIR/conc_py_1.json" | normalize_json)
  py_all_match=true
  for i in $(seq 2 10); do
    py_i=$(cat "$WORK_DIR/conc_py_$i.json" | normalize_json)
    if [[ "$py_first" != "$py_i" ]]; then
      py_all_match=false
      break
    fi
  done

  cpp_first=""
  cpp_all_match=true
  if $cpp_srv_ready; then
    cpp_first=$(cat "$WORK_DIR/conc_cpp_1.json" | normalize_json)
    for i in $(seq 2 10); do
      cpp_i=$(cat "$WORK_DIR/conc_cpp_$i.json" | normalize_json)
      if [[ "$cpp_first" != "$cpp_i" ]]; then
        cpp_all_match=false
        break
      fi
    done
  fi

  if [[ "$go_all_match" == "true" && "$java_all_match" == "true" && "$py_all_match" == "true" ]]; then
    conc_pass=$((conc_pass + 1))
    echo "    [1] All 10 concurrent responses identical within each engine"
  else
    fail "concurrent: responses differ within engine (Go=$go_all_match, Java=$java_all_match, Python=$py_all_match)"
  fi

  if $cpp_srv_ready; then
    cpp_conc_total=$((cpp_conc_total + 1))
    if [[ "$cpp_all_match" == "true" ]]; then
      cpp_conc_pass=$((cpp_conc_pass + 1))
      echo "    [1] C++ 10 concurrent responses identical"
    else
      fail "concurrent: C++ responses differ within engine"
    fi
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

  # Go and Python results should match
  conc_total=$((conc_total + 1))
  if [[ "$go_first" == "$py_first" ]]; then
    conc_pass=$((conc_pass + 1))
    echo "    [3] Go vs Python concurrent response → match"
  else
    fail "concurrent: Go vs Python divergence under concurrent load"
    diff <(echo "$go_first" | python3 -m json.tool) <(echo "$py_first" | python3 -m json.tool) >&2 || true
  fi

  # Go and C++ results should match
  if $cpp_srv_ready; then
    cpp_conc_total=$((cpp_conc_total + 1))
    if [[ "$go_first" == "$cpp_first" ]]; then
      cpp_conc_pass=$((cpp_conc_pass + 1))
      echo "    [4] Go vs C++ concurrent response → match"
    else
      fail "concurrent: Go vs C++ divergence under concurrent load"
      diff <(echo "$go_first" | python3 -m json.tool) <(echo "$cpp_first" | python3 -m json.tool) >&2 || true
    fi
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
  py_exec_counts=$(curl -s "http://localhost:$PY_CONC_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ops = d.get('operators', {})
counts = [v.get('exec_count', 0) for v in ops.values()]
print(min(counts), max(counts))
")

  if [[ "$go_exec_counts" == "10 10" && "$java_exec_counts" == "10 10" && "$py_exec_counts" == "10 10" ]]; then
    conc_pass=$((conc_pass + 1))
    echo "    [4] Stats exec_count = 10 for all operators (all engines)"
  else
    fail "concurrent: stats exec_count mismatch (Go=$go_exec_counts, Java=$java_exec_counts, Python=$py_exec_counts)"
  fi

  # C++ stats check
  if $cpp_srv_ready; then
    cpp_conc_total=$((cpp_conc_total + 1))
    cpp_exec_counts=$(curl -s "http://localhost:$CPP_CONC_PORT/stats" | python3 -c "
import json, sys
d = json.load(sys.stdin)
ops = d.get('operators', {})
counts = [v.get('exec_count', 0) for v in ops.values()]
print(min(counts), max(counts))
")
    if [[ "$cpp_exec_counts" == "10 10" ]]; then
      cpp_conc_pass=$((cpp_conc_pass + 1))
      echo "    [5] C++ stats exec_count = 10 for all operators"
    else
      fail "concurrent: C++ stats exec_count mismatch ($cpp_exec_counts)"
    fi
  fi

  kill $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  PY_SRV_PID=""
else
  fail "concurrent: servers failed to start"
  kill $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID $JAVA_SRV_PID $PY_SRV_PID 2>/dev/null || true
  [[ -n "$CPP_SRV_PID" ]] && wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  PY_SRV_PID=""
fi

if [[ $conc_total -gt 0 && $conc_pass -eq $conc_total ]]; then
  pass "concurrent request parity ($conc_pass/$conc_total checks)"
elif [[ $conc_total -eq 0 ]]; then
  pass "concurrent request parity (skipped)"
fi

if [[ -n "${CPP_SERVER:-}" ]]; then
  if [[ $cpp_conc_total -gt 0 && $cpp_conc_pass -eq $cpp_conc_total ]]; then
    pass "concurrent request parity Go vs C++ ($cpp_conc_pass/$cpp_conc_total checks)"
  elif [[ $cpp_conc_total -eq 0 ]]; then
    pass "concurrent request parity Go vs C++ (skipped)"
  fi
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
