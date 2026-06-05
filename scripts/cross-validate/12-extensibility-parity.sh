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

GO_EXT_PORT=22001
JAVA_EXT_PORT=22002
CPP_EXT_PORT=22004

# Start servers
"$WORK_DIR/pineapple-server" -config "$EXT_CONFIG" -addr ":$GO_EXT_PORT" &
GO_SRV_PID=$!

java -cp "$JAVA_CP" -Dpine.config="$EXT_CONFIG" -Dpine.port=$JAVA_EXT_PORT page.liam.pine.PineServer &
JAVA_SRV_PID=$!

CPP_SRV_PID=""
if [[ -n "${CPP_SERVER:-}" ]]; then
  "$CPP_SERVER" -config "$EXT_CONFIG" -addr ":$CPP_EXT_PORT" &
  CPP_SRV_PID=$!
fi

ext_cleanup() {
  [[ -n "${GO_SRV_PID:-}" ]] && kill $GO_SRV_PID 2>/dev/null || true
  [[ -n "${JAVA_SRV_PID:-}" ]] && kill $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "${CPP_SRV_PID:-}" ]] && kill $CPP_SRV_PID 2>/dev/null || true
  wait $GO_SRV_PID 2>/dev/null || true
  wait $JAVA_SRV_PID 2>/dev/null || true
  [[ -n "${CPP_SRV_PID:-}" ]] && wait $CPP_SRV_PID 2>/dev/null || true
  GO_SRV_PID=""
  JAVA_SRV_PID=""
  CPP_SRV_PID=""
}
trap 'ext_cleanup' EXIT

ext_pass=0
ext_total=0
cpp_ext_pass=0
cpp_ext_total=0
cpp_srv_ready=false

if ! srv_ready $GO_EXT_PORT; then
  fail "extensibility: Go server failed to start"
  ext_cleanup
elif ! srv_ready $JAVA_EXT_PORT; then
  fail "extensibility: Java server failed to start"
  ext_cleanup
else
  echo "    All three servers ready."
  if [[ -n "${CPP_SERVER:-}" ]] && srv_ready $CPP_EXT_PORT; then
    cpp_srv_ready=true
    echo "    C++ server also ready."
  fi

  # Test 1: GET /unknown → 404 status code parity
  ext_total=$((ext_total + 1))
  go_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_EXT_PORT/nonexistent")
  java_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_EXT_PORT/nonexistent")
  if [[ "$go_404_code" == "404" && "$java_404_code" == "404" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [1] GET /nonexistent → 404 (all three engines)"
  else
    fail "extensibility: 404 status code (Go=$go_404_code, Java=$java_404_code)"
  fi

  if $cpp_srv_ready; then
    cpp_ext_total=$((cpp_ext_total + 1))
    cpp_404_code=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$CPP_EXT_PORT/nonexistent")
    if [[ "$cpp_404_code" == "404" ]]; then
      cpp_ext_pass=$((cpp_ext_pass + 1))
      echo "    [1] C++ GET /nonexistent → 404"
    else
      fail "extensibility: C++ 404 status code ($cpp_404_code)"
    fi
  fi

  # Test 2: GET /unknown → JSON body {"error":"not found"} parity
  ext_total=$((ext_total + 1))
  go_404_body=$(curl -s "http://localhost:$GO_EXT_PORT/nonexistent" | normalize_json)
  java_404_body=$(curl -s "http://localhost:$JAVA_EXT_PORT/nonexistent" | normalize_json)
  if [[ "$go_404_body" == "$java_404_body" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [2] GET /nonexistent → JSON body identical across all engines"
  else
    fail "extensibility: 404 body divergence (Go=$go_404_body, Java=$java_404_body)"
  fi

  if $cpp_srv_ready; then
    cpp_ext_total=$((cpp_ext_total + 1))
    cpp_404_body=$(curl -s "http://localhost:$CPP_EXT_PORT/nonexistent" | normalize_json)
    if [[ "$go_404_body" == "$cpp_404_body" ]]; then
      cpp_ext_pass=$((cpp_ext_pass + 1))
      echo "    [2] C++ GET /nonexistent → JSON body matches Go"
    else
      fail "extensibility: C++ 404 body divergence (Go=$go_404_body, C++=$cpp_404_body)"
    fi
  fi

  # Test 3: GET /unknown → Content-Type is application/json
  ext_total=$((ext_total + 1))
  go_404_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$GO_EXT_PORT/nonexistent")
  java_404_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$JAVA_EXT_PORT/nonexistent")
  all_json=true
  for ct in "$go_404_ct" "$java_404_ct"; do
    if [[ "$ct" != *"application/json"* ]]; then
      all_json=false
      break
    fi
  done
  if $all_json; then
    ext_pass=$((ext_pass + 1))
    echo "    [3] GET /nonexistent → Content-Type: application/json (all engines)"
  else
    fail "extensibility: 404 Content-Type (Go=$go_404_ct, Java=$java_404_ct)"
  fi

  if $cpp_srv_ready; then
    cpp_ext_total=$((cpp_ext_total + 1))
    cpp_404_ct=$(curl -s -o /dev/null -w "%{content_type}" "http://localhost:$CPP_EXT_PORT/nonexistent")
    if [[ "$cpp_404_ct" == *"application/json"* ]]; then
      cpp_ext_pass=$((cpp_ext_pass + 1))
      echo "    [3] C++ GET /nonexistent → Content-Type: application/json"
    else
      fail "extensibility: C++ 404 Content-Type ($cpp_404_ct)"
    fi
  fi

  # Test 4: POST /unknown → 404 JSON (not 405)
  ext_total=$((ext_total + 1))
  go_post404_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$GO_EXT_PORT/nonexistent")
  java_post404_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$JAVA_EXT_PORT/nonexistent")
  go_post404_body=$(curl -s -X POST "http://localhost:$GO_EXT_PORT/nonexistent" | normalize_json)
  java_post404_body=$(curl -s -X POST "http://localhost:$JAVA_EXT_PORT/nonexistent" | normalize_json)
  if [[ "$go_post404_code" == "$java_post404_code" &&
        "$go_post404_body" == "$java_post404_body" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [4] POST /nonexistent → $go_post404_code + body parity (all engines)"
  else
    fail "extensibility: POST unknown (Go=$go_post404_code/$go_post404_body, Java=$java_post404_code/$java_post404_body)"
  fi

  if $cpp_srv_ready; then
    cpp_ext_total=$((cpp_ext_total + 1))
    cpp_post404_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://localhost:$CPP_EXT_PORT/nonexistent")
    cpp_post404_body=$(curl -s -X POST "http://localhost:$CPP_EXT_PORT/nonexistent" | normalize_json)
    if [[ "$go_post404_code" == "$cpp_post404_code" && "$go_post404_body" == "$cpp_post404_body" ]]; then
      cpp_ext_pass=$((cpp_ext_pass + 1))
      echo "    [4] C++ POST /nonexistent → $cpp_post404_code matches Go"
    else
      fail "extensibility: C++ POST unknown (Go=$go_post404_code/$go_post404_body, C++=$cpp_post404_code/$cpp_post404_body)"
    fi
  fi

  # Test 5: Multiple unknown paths return same response format
  ext_total=$((ext_total + 1))
  paths_ok=true
  for upath in "/foo" "/bar/baz" "/api/v2/test" "/metrics-not-registered"; do
    g=$(curl -s "http://localhost:$GO_EXT_PORT$upath" | normalize_json)
    j=$(curl -s "http://localhost:$JAVA_EXT_PORT$upath" | normalize_json)
    if [[ "$g" != "$j" ]]; then
      paths_ok=false
      fail "extensibility: path $upath divergence (Go=$g, Java=$j)"
      break
    fi
  done
  if $paths_ok; then
    ext_pass=$((ext_pass + 1))
    echo "    [5] Multiple unknown paths → consistent 404 JSON (all engines)"
  fi

  if $cpp_srv_ready; then
    cpp_ext_total=$((cpp_ext_total + 1))
    cpp_paths_ok=true
    for upath in "/foo" "/bar/baz" "/api/v2/test" "/metrics-not-registered"; do
      g=$(curl -s "http://localhost:$GO_EXT_PORT$upath" | normalize_json)
      c=$(curl -s "http://localhost:$CPP_EXT_PORT$upath" | normalize_json)
      if [[ "$g" != "$c" ]]; then
        cpp_paths_ok=false
        fail "extensibility: C++ path $upath divergence (Go=$g, C++=$c)"
        break
      fi
    done
    if $cpp_paths_ok; then
      cpp_ext_pass=$((cpp_ext_pass + 1))
      echo "    [5] C++ multiple unknown paths → consistent 404 JSON"
    fi
  fi

  # Test 6: Deep nested unknown path → 404 (no prefix match issues)
  ext_total=$((ext_total + 1))
  go_deep=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_EXT_PORT/health/sub/path")
  java_deep=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_EXT_PORT/health/sub/path")
  if [[ "$go_deep" == "$java_deep" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [6] GET /health/sub/path → $go_deep (all engines, no prefix match leak)"
  else
    fail "extensibility: deep path (Go=$go_deep, Java=$java_deep)"
  fi

  if $cpp_srv_ready; then
    cpp_ext_total=$((cpp_ext_total + 1))
    cpp_deep=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$CPP_EXT_PORT/health/sub/path")
    if [[ "$go_deep" == "$cpp_deep" ]]; then
      cpp_ext_pass=$((cpp_ext_pass + 1))
      echo "    [6] C++ GET /health/sub/path → $cpp_deep matches Go"
    else
      fail "extensibility: C++ deep path (Go=$go_deep, C++=$cpp_deep)"
    fi
  fi

  # Test 7: /debug/pprof/* must NOT be on the main port for any runtime.
  # pine-go ships net/http/pprof on a separate AdminAddr (default off);
  # pine-java and pine-cpp have no equivalent feature. Asserting 404 here
  # documents pprof as a pine-go-only side-port concern and catches a
  # future regression that wires it onto the public listener (audit M11).
  ext_total=$((ext_total + 1))
  go_pprof=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$GO_EXT_PORT/debug/pprof/")
  java_pprof=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$JAVA_EXT_PORT/debug/pprof/")
  if [[ "$go_pprof" == "404" && "$java_pprof" == "404" ]]; then
    ext_pass=$((ext_pass + 1))
    echo "    [7] GET /debug/pprof/ → 404 (Go without AdminAddr, Java has no pprof)"
  else
    fail "extensibility: /debug/pprof/ must be 404 on main port (Go=$go_pprof, Java=$java_pprof)"
  fi

  if $cpp_srv_ready; then
    cpp_ext_total=$((cpp_ext_total + 1))
    cpp_pprof=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:$CPP_EXT_PORT/debug/pprof/")
    if [[ "$cpp_pprof" == "404" ]]; then
      cpp_ext_pass=$((cpp_ext_pass + 1))
      echo "    [7] C++ GET /debug/pprof/ → 404 (no pprof feature)"
    else
      fail "extensibility: C++ /debug/pprof/ must be 404 ($cpp_pprof)"
    fi
  fi

  ext_cleanup
fi

if [[ $ext_total -gt 0 && $ext_pass -eq $ext_total ]]; then
  pass "extensibility parity ($ext_pass/$ext_total checks)"
elif [[ $ext_total -eq 0 ]]; then
  pass "extensibility parity (skipped)"
fi

if [[ -n "${CPP_SERVER:-}" ]]; then
  if [[ $cpp_ext_total -gt 0 && $cpp_ext_pass -eq $cpp_ext_total ]]; then
    pass "extensibility parity Go vs C++ ($cpp_ext_pass/$cpp_ext_total checks)"
  elif [[ $cpp_ext_total -eq 0 ]]; then
    pass "extensibility parity Go vs C++ (skipped)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
