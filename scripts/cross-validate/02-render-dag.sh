#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_parallel.sh"

# ---------- 2. Render-DAG parity ----------
echo
echo "==> [2/$TOTAL_SECTIONS] Render-DAG parity"

dag_pass=0
dag_total=0
cpp_dag_pass=0
cpp_dag_total=0

for fixture in "$REPO_ROOT"/fixtures/pipelines/*.json; do
  [[ -f "$fixture" ]] || continue
  fname=$(basename "$fixture")

  # Skip fixtures that need external prepopulated state (redis) or
  # specially-built bench-tag binaries. Production binaries (used here)
  # don't register bench stubs, so render-dag would error out.
  if python3 -c "
import json, sys
data = json.load(open('$fixture'))
req = set(data.get('requires', []) or [])
sys.exit(0 if req & {'redis', 'redis-unavailable', 'bench'} else 1)
"; then
    continue
  fi

  config_file="$WORK_DIR/dag_config_${fname}"
  python3 -c "
import json
with open('$fixture') as f:
    data = json.load(f)
cfg = data.get('config', data)
with open('$config_file', 'w') as cf:
    json.dump(cfg, cf)
" || { fail "render-dag extract config: $fname"; continue; }

  has_pipeline_map=false
  grep -q '"pipeline_map"' "$fixture" 2>/dev/null && has_pipeline_map=true

  # Fire all renders in parallel
  dag_dir="$WORK_DIR/dag_${fname}"
  mkdir -p "$dag_dir"

  { _rc=0; "$WORK_DIR/pineapple-dag" -config "$config_file" -format dot > "$dag_dir/go.dot" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/go.dot.rc"; } &
  { _rc=0; "$WORK_DIR/pineapple-dag" -config "$config_file" -format mermaid > "$dag_dir/go.mmd" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/go.mmd.rc"; } &
  if [[ -n "${CPP_DAG:-}" ]]; then
    { _rc=0; "$CPP_DAG" -config "$config_file" -format dot > "$dag_dir/cpp.dot" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/cpp.dot.rc"; } &
    { _rc=0; "$CPP_DAG" -config "$config_file" -format mermaid > "$dag_dir/cpp.mmd" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/cpp.mmd.rc"; } &
  fi
  # JVM serial: avoid truncated output under CI resource pressure
  _rc=0; java_run page.liam.pine.RenderDAGCli -config "$config_file" -format dot > "$dag_dir/java.dot" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/java.dot.rc"
  _rc=0; java_run page.liam.pine.RenderDAGCli -config "$config_file" -format mermaid > "$dag_dir/java.mmd" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/java.mmd.rc"

  if [[ "$has_pipeline_map" == "true" ]]; then
    for cl in 1 2; do
      { _rc=0; "$WORK_DIR/pineapple-dag" -config "$config_file" -format dot -collapse "$cl" > "$dag_dir/go.dot.c${cl}" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/go.dot.c${cl}.rc"; } &
      { _rc=0; "$WORK_DIR/pineapple-dag" -config "$config_file" -format mermaid -collapse "$cl" > "$dag_dir/go.mmd.c${cl}" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/go.mmd.c${cl}.rc"; } &
      if [[ -n "${CPP_DAG:-}" ]]; then
        { _rc=0; "$CPP_DAG" -config "$config_file" -format dot -collapse "$cl" > "$dag_dir/cpp.dot.c${cl}" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/cpp.dot.c${cl}.rc"; } &
        { _rc=0; "$CPP_DAG" -config "$config_file" -format mermaid -collapse "$cl" > "$dag_dir/cpp.mmd.c${cl}" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/cpp.mmd.c${cl}.rc"; } &
      fi
      _rc=0; java_run page.liam.pine.RenderDAGCli -config "$config_file" -format dot -collapse "$cl" > "$dag_dir/java.dot.c${cl}" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/java.dot.c${cl}.rc"
      _rc=0; java_run page.liam.pine.RenderDAGCli -config "$config_file" -format mermaid -collapse "$cl" > "$dag_dir/java.mmd.c${cl}" 2>/dev/null || _rc=$?; echo "$_rc" > "$dag_dir/java.mmd.c${cl}.rc"
    done
  fi

  wait

  # Compare results
  compare_dag() {
    local label="$1" go_file="$2" java_file="$3" cpp_file="$4"
    dag_total=$((dag_total + 1))
    if [[ -n "${CPP_DAG:-}" ]]; then
      cpp_dag_total=$((cpp_dag_total + 1))
    fi

    if [[ -f "${go_file}.rc" && "$(cat "${go_file}.rc")" != "0" ]]; then
      fail "render-dag Go crashed (rc=$(cat "${go_file}.rc")): $fname ($label)"; return
    fi
    if [[ ! -s "$go_file" ]]; then
      fail "render-dag Go failed: $fname ($label)"; return
    fi

    if [[ -f "${java_file}.rc" && "$(cat "${java_file}.rc")" != "0" ]]; then
      fail "render-dag Java crashed (rc=$(cat "${java_file}.rc")): $fname ($label)"; return
    fi
    if [[ ! -s "$java_file" ]]; then
      fail "render-dag Java failed: $fname ($label)"; return
    fi

    if cmp -s "$go_file" "$java_file"; then
      dag_pass=$((dag_pass + 1))
      echo "    [$dag_total] $fname ($label) Go vs Java → match"
    else
      fail "render-dag divergence (Go vs Java): $fname ($label)"
    fi

    if [[ -n "${CPP_DAG:-}" ]]; then
      if [[ -f "${cpp_file}.rc" && "$(cat "${cpp_file}.rc")" != "0" ]]; then
        fail "render-dag C++ crashed (rc=$(cat "${cpp_file}.rc")): $fname ($label)"; return
      fi
      if [[ ! -s "$cpp_file" ]]; then
        fail "render-dag C++ failed: $fname ($label)"; return
      fi
      if cmp -s "$go_file" "$cpp_file"; then
        cpp_dag_pass=$((cpp_dag_pass + 1))
        echo "    [$dag_total] $fname ($label) Go vs C++ → match"
      else
        fail "render-dag divergence (Go vs C++): $fname ($label)"
      fi
    fi
  }

  compare_dag "dot" "$dag_dir/go.dot" "$dag_dir/java.dot" "$dag_dir/cpp.dot"
  compare_dag "mermaid" "$dag_dir/go.mmd" "$dag_dir/java.mmd" "$dag_dir/cpp.mmd"

  if [[ "$has_pipeline_map" == "true" ]]; then
    for cl in 1 2; do
      compare_dag "dot collapse=$cl" "$dag_dir/go.dot.c${cl}" "$dag_dir/java.dot.c${cl}" "$dag_dir/cpp.dot.c${cl}"
      compare_dag "mermaid collapse=$cl" "$dag_dir/go.mmd.c${cl}" "$dag_dir/java.mmd.c${cl}" "$dag_dir/cpp.mmd.c${cl}"
    done
  fi
done

if [[ $dag_total -gt 0 && $dag_pass -eq $dag_total ]]; then
  pass "render-dag parity Go vs Java ($dag_pass/$dag_total fixtures)"
elif [[ $dag_total -eq 0 ]]; then
  pass "render-dag parity Go vs Java (no fixtures found, skipped)"
fi

if [[ -n "${CPP_DAG:-}" ]]; then
  if [[ $cpp_dag_total -gt 0 && $cpp_dag_pass -eq $cpp_dag_total ]]; then
    pass "render-dag parity Go vs C++ ($cpp_dag_pass/$cpp_dag_total fixtures)"
  elif [[ $cpp_dag_total -eq 0 ]]; then
    pass "render-dag parity Go vs C++ (no fixtures found, skipped)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
