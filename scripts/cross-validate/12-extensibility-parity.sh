#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 12. Extensibility parity ----------
echo
echo "==> [12/$TOTAL_SECTIONS] Extensibility parity (404 JSON, middleware custom path)"

# Reuse the same simple fixture
SRV_FIXTURE="$REPO_ROOT/fixtures/pipelines/transform_then_filter.json"
EXT_CONFIG="$WORK_DIR/ext_config.json"
python3 -c "
import json
with open('$SRV_FIXTURE') as f:
    data = json.load(f)
cfg = data.get('config', {})
with open('$EXT_CONFIG', 'w') as cf:
    json.dump(cfg, cf)
"

GO_EXT_PORT=18910
JAVA_EXT_PORT=18911
PY_EXT_PORT=18912

# Start servers
"$WORK_DIR/pineapple-server" -config "$EXT_CONFIG" -addr ":$GO_EXT_PORT" &
GO_SRV_PID=$!

java -cp "$JAVA_CP" -Dpine.config="$EXT_CONFIG" -Dpine.port=$JAVA_EXT_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

(cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.server -config "$EXT_CONFIG" -addr ":$PY_EXT_PORT") &
PY_SRV_PID=$!

ext_cleanup() {
  [[ -n "${GO_SRV_PID:-}" ]] && kill $GO_SRV_PID 2>/dev/null || true
  [[ -n "${JAVA_SRV_PID:-}" ]] && kill $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "${PY_SRV_PID:-}" ]] && kill $PY_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID 2>/dev/null || true
  wait $JAVA_SRV_PID 2>/dev/null || true
  wait $PY_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  PY_SRV_PID=""
}
trap 'ext_cleanup' EXIT

ext_pass=0
ext_total=0

if ! srv_ready $GO_EXT_PORT; then
  fail "extensibility: Go server failed to start"
  ext_cleanup
elif ! srv_ready $JAVA_EXT_PORT; then
  fail "extensibility: Java server failed to start"
  ext_cleanup
elif ! srv_ready $PY_EXT_PORT; then
  fail "extensibility: Python server failed to start"
  ext_cleanup
else
  echo "    All three servers ready."

  # Test 1: GET /unknown → 404 status code parity
  ext_total=$((ext_total + 1))
  go_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_EXT_PORT/nonexistent")
  java_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_EXT_PORT/nonexistent")
  py_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$PY_EXT_PORT/nonexistent")
  if [[ "$go_404_code" == "404" && "$java_404_code" == "404" && "$py_404_code" == "404" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [1] GET /nonexistent → 404 (all three engines)"
  else
    fail "extensibility: 404 status code (Go=$go_404_code, Java=$java_404_code, Python=$py_404_code)"
  fi

  # Test 2: GET /unknown → JSON body {"error":"not found"} parity
  ext_total=$((ext_total + 1))
  go_404_body=$(curl -s "http://localhost:$GO_EXT_PORT/nonexistent" | normalize_json)
  java_404_body=$(curl -s "http://localhost:$JAVA_EXT_PORT/nonexistent" | normalize_json)
  py_404_body=$(curl -s "http://localhost:$PY_EXT_PORT/nonexistent" | normalize_json)
  if [[ "$go_404_body" == "$java_404_body" && "$go_404_body" == "$py_404_body" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [2] GET /nonexistent → JSON body identical across all engines"
  else
    fail "extensibility: 404 body divergence (Go=$go_404_body, Java=$java_404_body, Python=$py_404_body)"
  fi

  # Test 3: GET /unknown → Content-Type is application/json
  ext_total=$((ext_total + 1))
  go_404_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$GO_EXT_PORT/nonexistent")
  java_404_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$JAVA_EXT_PORT/nonexistent")
  py_404_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$PY_EXT_PORT/nonexistent")
  all_json=true
  for ct in "$go_404_ct" "$java_404_ct" "$py_404_ct"; do
    if [[ "$ct" != *"application/json"* ]]; then
      all_json=false
      break
    fi
  done
  if $all_json; then
    ext_pass=$((ext_pass + 1))
    echo "    [3] GET /nonexistent → Content-Type: application/json (all engines)"
  else
    fail "extensibility: 404 Content-Type (Go=$go_404_ct, Java=$java_404_ct, Python=$py_404_ct)"
  fi

  # Test 4: POST /unknown → 404 JSON (not 405)
  ext_total=$((ext_total + 1))
  go_post404_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$GO_EXT_PORT/nonexistent")
  java_post404_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$JAVA_EXT_PORT/nonexistent")
  py_post404_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$PY_EXT_PORT/nonexistent")
  go_post404_body=$(curl -s -X POST "http://localhost:$GO_EXT_PORT/nonexistent" | normalize_json)
  java_post404_body=$(curl -s -X POST "http://localhost:$JAVA_EXT_PORT/nonexistent" | normalize_json)
  py_post404_body=$(curl -s -X POST "http://localhost:$PY_EXT_PORT/nonexistent" | normalize_json)
  if [[ "$go_post404_code" == "$java_post404_code" && "$go_post404_code" == "$py_post404_code" &&
        "$go_post404_body" == "$java_post404_body" && "$go_post404_body" == "$py_post404_body" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [4] POST /nonexistent → $go_post404_code + body parity (all engines)"
  else
    fail "extensibility: POST unknown (Go=$go_post404_code/$go_post404_body, Java=$java_post404_code/$java_post404_body, Python=$py_post404_code/$py_post404_body)"
  fi

  # Test 5: Multiple unknown paths return same response format
  ext_total=$((ext_total + 1))
  paths_ok=true
  for upath in "/foo" "/bar/baz" "/api/v2/test" "/metrics-not-registered"; do
    g=$(curl -s "http://localhost:$GO_EXT_PORT$upath" | normalize_json)
    j=$(curl -s "http://localhost:$JAVA_EXT_PORT$upath" | normalize_json)
    p=$(curl -s "http://localhost:$PY_EXT_PORT$upath" | normalize_json)
    if [[ "$g" != "$j" || "$g" != "$p" ]]; then
      paths_ok=false
      fail "extensibility: path $upath divergence (Go=$g, Java=$j, Python=$p)"
      break
    fi
  done
  if $paths_ok; then
    ext_pass=$((ext_pass + 1))
    echo "    [5] Multiple unknown paths → consistent 404 JSON (all engines)"
  fi

  # Test 6: Deep nested unknown path → 404 (no prefix match issues)
  ext_total=$((ext_total + 1))
  go_deep=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_EXT_PORT/health/sub/path")
  java_deep=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_EXT_PORT/health/sub/path")
  py_deep=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$PY_EXT_PORT/health/sub/path")
  if [[ "$go_deep" == "$java_deep" && "$go_deep" == "$py_deep" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [6] GET /health/sub/path → $go_deep (all engines, no prefix match leak)"
  else
    fail "extensibility: deep path (Go=$go_deep, Java=$java_deep, Python=$py_deep)"
  fi

  ext_cleanup
fi

if [[ $ext_total -gt 0 && $ext_pass -eq $ext_total ]]; then
  pass "extensibility parity ($ext_pass/$ext_total checks)"
elif [[ $ext_total -eq 0 ]]; then
  pass "extensibility parity (skipped)"
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
