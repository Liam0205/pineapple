#!/usr/bin/env bash
# Section 14: Byte-exact server /execute JSON parity (Go vs Java vs Python vs C++)
#
# For each fixture under fixtures/server_byte_exact/*.json:
#   { "config": {...}, "request": {...} }
# spin up each engine's HTTP server, POST the request, and compare the raw
# response body byte-by-byte. This catches structural diffs the looser
# 06-server-http checks would miss (e.g. partial-result on execution error,
# omitempty boundaries, nil vs empty-map serialization).
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

echo
echo "==> [14/$TOTAL_SECTIONS] Byte-exact /execute JSON parity (Go vs Java vs Python vs C++)"

FIXTURE_DIR="$REPO_ROOT/fixtures/server_byte_exact"
if [[ ! -d "$FIXTURE_DIR" ]]; then
  echo "    (no fixtures under $FIXTURE_DIR, skipping)"
  pass "byte-exact /execute parity Go vs Java (no fixtures)"
  pass "byte-exact /execute parity Go vs Python (no fixtures)"
  pass "byte-exact /execute parity Go vs C++ (no fixtures)"
  return 0
fi

GO_PORT=18931
JAVA_PORT=18932
PY_PORT=18933
CPP_PORT=18934

WORK_BX="$WORK_DIR/byte_exact"
mkdir -p "$WORK_BX"

bx_pass_java=0
bx_total_java=0
bx_pass_py=0
bx_total_py=0
bx_pass_cpp=0
bx_total_cpp=0

for fixture_file in "$FIXTURE_DIR"/*.json; do
  [[ -f "$fixture_file" ]] || continue
  fname=$(basename "$fixture_file" .json)
  bx_total_java=$((bx_total_java + 1))
  bx_total_py=$((bx_total_py + 1))
  [[ -n "${CPP_SERVER:-}" ]] && bx_total_cpp=$((bx_total_cpp + 1))

  cfg_file="$WORK_BX/${fname}_config.json"
  req_file="$WORK_BX/${fname}_req.json"
  python3 -c "
import json
with open('$fixture_file') as f:
    data = json.load(f)
with open('$cfg_file', 'w') as cf:
    json.dump(data['config'], cf)
with open('$req_file', 'w') as rf:
    json.dump(data['request'], rf)
" 2>/dev/null || { fail "byte-exact: parse $fname"; continue; }

  # Start Go server
  "$WORK_DIR/pineapple-server" -config "$cfg_file" -addr ":$GO_PORT" >/dev/null 2>&1 &
  GO_PID=$!
  # Start Java server
  java -cp "$JAVA_CP" -Dpine.config="$cfg_file" -Dpine.port=$JAVA_PORT page.liam.pine.PineServer >/dev/null 2>&1 &
  JAVA_PID=$!
  # Start Python server (note: subshell-with-cd + & loses pid; pushd/popd inside main shell)
  (cd "$REPO_ROOT/pine-python" && exec python3 -m pine.cli.server -config "$cfg_file" -addr ":$PY_PORT") >/dev/null 2>&1 &
  PY_PID=$!
  # Start C++ server (optional)
  CPP_PID=""
  if [[ -n "${CPP_SERVER:-}" ]]; then
    "$CPP_SERVER" -config "$cfg_file" -addr ":$CPP_PORT" >/dev/null 2>&1 &
    CPP_PID=$!
  fi

  bx_cleanup() {
    [[ -n "${GO_PID:-}" ]] && kill $GO_PID 2>/dev/null || true
    [[ -n "${JAVA_PID:-}" ]] && kill $JAVA_PID 2>/dev/null || true
    [[ -n "${PY_PID:-}" ]] && kill $PY_PID 2>/dev/null || true
    [[ -n "${CPP_PID:-}" ]] && kill $CPP_PID 2>/dev/null || true
    wait $GO_PID 2>/dev/null || true
    wait $JAVA_PID 2>/dev/null || true
    wait $PY_PID 2>/dev/null || true
    [[ -n "${CPP_PID:-}" ]] && wait $CPP_PID 2>/dev/null || true
    # Belt and suspenders: kill anything still lingering on these ports so we
    # don't accidentally curl an old fixture's server on retry/repeat.
    for port in $GO_PORT $JAVA_PORT $PY_PORT $CPP_PORT; do
      lsof -ti :"$port" 2>/dev/null | xargs -r kill -9 2>/dev/null || true
    done
    sleep 0.3
  }

  srv_ready $GO_PORT || { fail "byte-exact $fname: Go server not ready"; bx_cleanup; continue; }
  srv_ready $JAVA_PORT || { fail "byte-exact $fname: Java server not ready"; bx_cleanup; continue; }
  srv_ready $PY_PORT || { fail "byte-exact $fname: Python server not ready"; bx_cleanup; continue; }
  if [[ -n "${CPP_SERVER:-}" ]]; then
    srv_ready $CPP_PORT || { fail "byte-exact $fname: C++ server not ready"; bx_cleanup; continue; }
  fi

  go_resp=$(curl -sS -X POST "http://localhost:$GO_PORT/execute" -H "Content-Type: application/json" --data @"$req_file")
  java_resp=$(curl -sS -X POST "http://localhost:$JAVA_PORT/execute" -H "Content-Type: application/json" --data @"$req_file")
  py_resp=$(curl -sS -X POST "http://localhost:$PY_PORT/execute" -H "Content-Type: application/json" --data @"$req_file")
  cpp_resp=""
  if [[ -n "${CPP_SERVER:-}" ]]; then
    cpp_resp=$(curl -sS -X POST "http://localhost:$CPP_PORT/execute" -H "Content-Type: application/json" --data @"$req_file")
  fi

  bx_cleanup

  if [[ "$go_resp" == "$java_resp" ]]; then
    bx_pass_java=$((bx_pass_java + 1))
    echo "    [$fname] /execute Go vs Java → byte-exact"
  else
    fail "byte-exact /execute Go vs Java mismatch in $fname"
    echo "      Go:   $go_resp" | head -1 >&2
    echo "      Java: $java_resp" | head -1 >&2
  fi

  if [[ "$go_resp" == "$py_resp" ]]; then
    bx_pass_py=$((bx_pass_py + 1))
    echo "    [$fname] /execute Go vs Python → byte-exact"
  else
    fail "byte-exact /execute Go vs Python mismatch in $fname"
    echo "      Py:   $py_resp" | head -1 >&2
  fi

  if [[ -n "${CPP_SERVER:-}" ]]; then
    if [[ "$go_resp" == "$cpp_resp" ]]; then
      bx_pass_cpp=$((bx_pass_cpp + 1))
      echo "    [$fname] /execute Go vs C++ → byte-exact"
    else
      fail "byte-exact /execute Go vs C++ mismatch in $fname"
      echo "      C++:  $cpp_resp" | head -1 >&2
    fi
  fi

done

if [[ $bx_total_java -eq 0 ]]; then
  pass "byte-exact /execute parity Go vs Java (no fixtures)"
elif [[ $bx_pass_java -eq $bx_total_java ]]; then
  pass "byte-exact /execute parity Go vs Java ($bx_pass_java/$bx_total_java)"
fi

if [[ $bx_total_py -eq 0 ]]; then
  pass "byte-exact /execute parity Go vs Python (no fixtures)"
elif [[ $bx_pass_py -eq $bx_total_py ]]; then
  pass "byte-exact /execute parity Go vs Python ($bx_pass_py/$bx_total_py)"
fi

if [[ -n "${CPP_SERVER:-}" ]]; then
  if [[ $bx_total_cpp -eq 0 ]]; then
    pass "byte-exact /execute parity Go vs C++ (no fixtures)"
  elif [[ $bx_pass_cpp -eq $bx_total_cpp ]]; then
    pass "byte-exact /execute parity Go vs C++ ($bx_pass_cpp/$bx_total_cpp)"
  fi
fi
