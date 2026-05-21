#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_parallel.sh"

# ---------- 2. Render-DAG parity ----------
echo
echo "==> [2/$TOTAL_SECTIONS] Render-DAG parity"

dag_pass=0
dag_total=0
py_dag_pass=0
py_dag_total=0
cpp_dag_pass=0
cpp_dag_total=0

for fixture in "$REPO_ROOT"/fixtures/pipelines/*.json; do
  [[ -f "$fixture" ]] || continue
  fname=$(basename "$fixture")

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

  "$WORK_DIR/pineapple-dag" -config "$config_file" -format dot > "$dag_dir/go.dot" 2>/dev/null &
  "$WORK_DIR/pineapple-dag" -config "$config_file" -format mermaid > "$dag_dir/go.mmd" 2>/dev/null &
  java_run page.liam.pine.RenderDAGCli -config "$config_file" -format dot > "$dag_dir/java.dot" 2>/dev/null &
  java_run page.liam.pine.RenderDAGCli -config "$config_file" -format mermaid > "$dag_dir/java.mmd" 2>/dev/null &
  (cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.dag -config "$config_file" -format dot) > "$dag_dir/py.dot" 2>/dev/null &
  (cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.dag -config "$config_file" -format mermaid) > "$dag_dir/py.mmd" 2>/dev/null &
  if [[ -n "${CPP_DAG:-}" ]]; then
    "$CPP_DAG" -config "$config_file" -format dot > "$dag_dir/cpp.dot" 2>/dev/null &
    "$CPP_DAG" -config "$config_file" -format mermaid > "$dag_dir/cpp.mmd" 2>/dev/null &
  fi

  if [[ "$has_pipeline_map" == "true" ]]; then
    for cl in 1 2; do
      "$WORK_DIR/pineapple-dag" -config "$config_file" -format dot -collapse "$cl" > "$dag_dir/go.dot.c${cl}" 2>/dev/null &
      "$WORK_DIR/pineapple-dag" -config "$config_file" -format mermaid -collapse "$cl" > "$dag_dir/go.mmd.c${cl}" 2>/dev/null &
      java_run page.liam.pine.RenderDAGCli -config "$config_file" -format dot -collapse "$cl" > "$dag_dir/java.dot.c${cl}" 2>/dev/null &
      java_run page.liam.pine.RenderDAGCli -config "$config_file" -format mermaid -collapse "$cl" > "$dag_dir/java.mmd.c${cl}" 2>/dev/null &
      (cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.dag -config "$config_file" -format dot -collapse "$cl") > "$dag_dir/py.dot.c${cl}" 2>/dev/null &
      (cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.dag -config "$config_file" -format mermaid -collapse "$cl") > "$dag_dir/py.mmd.c${cl}" 2>/dev/null &
      if [[ -n "${CPP_DAG:-}" ]]; then
        "$CPP_DAG" -config "$config_file" -format dot -collapse "$cl" > "$dag_dir/cpp.dot.c${cl}" 2>/dev/null &
        "$CPP_DAG" -config "$config_file" -format mermaid -collapse "$cl" > "$dag_dir/cpp.mmd.c${cl}" 2>/dev/null &
      fi
    done
  fi

  wait

  # Compare results
  compare_dag() {
    local label="$1" go_file="$2" java_file="$3" py_file="$4" cpp_file="$5"
    dag_total=$((dag_total + 1))
    py_dag_total=$((py_dag_total + 1))
    if [[ -n "${CPP_DAG:-}" ]]; then
      cpp_dag_total=$((cpp_dag_total + 1))
    fi

    if [[ ! -s "$go_file" ]]; then
      fail "render-dag Go failed: $fname ($label)"; return
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

    if [[ ! -s "$py_file" ]]; then
      fail "render-dag Python failed: $fname ($label)"; return
    fi

    if cmp -s "$go_file" "$py_file"; then
      py_dag_pass=$((py_dag_pass + 1))
      echo "    [$dag_total] $fname ($label) Go vs Python → match"
    else
      fail "render-dag divergence (Go vs Python): $fname ($label)"
    fi

    if [[ -n "${CPP_DAG:-}" ]]; then
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

  compare_dag "dot" "$dag_dir/go.dot" "$dag_dir/java.dot" "$dag_dir/py.dot" "$dag_dir/cpp.dot"
  compare_dag "mermaid" "$dag_dir/go.mmd" "$dag_dir/java.mmd" "$dag_dir/py.mmd" "$dag_dir/cpp.mmd"

  if [[ "$has_pipeline_map" == "true" ]]; then
    for cl in 1 2; do
      compare_dag "dot collapse=$cl" "$dag_dir/go.dot.c${cl}" "$dag_dir/java.dot.c${cl}" "$dag_dir/py.dot.c${cl}" "$dag_dir/cpp.dot.c${cl}"
      compare_dag "mermaid collapse=$cl" "$dag_dir/go.mmd.c${cl}" "$dag_dir/java.mmd.c${cl}" "$dag_dir/py.mmd.c${cl}" "$dag_dir/cpp.mmd.c${cl}"
    done
  fi
done

if [[ $dag_total -gt 0 && $dag_pass -eq $dag_total ]]; then
  pass "render-dag parity Go vs Java ($dag_pass/$dag_total fixtures)"
elif [[ $dag_total -eq 0 ]]; then
  pass "render-dag parity Go vs Java (no fixtures found, skipped)"
fi

if [[ $py_dag_total -gt 0 && $py_dag_pass -eq $py_dag_total ]]; then
  pass "render-dag parity Go vs Python ($py_dag_pass/$py_dag_total fixtures)"
elif [[ $py_dag_total -eq 0 ]]; then
  pass "render-dag parity Go vs Python (no fixtures found, skipped)"
fi

if [[ -n "${CPP_DAG:-}" ]]; then
  if [[ $cpp_dag_total -gt 0 && $cpp_dag_pass -eq $cpp_dag_total ]]; then
    pass "render-dag parity Go vs C++ ($cpp_dag_pass/$cpp_dag_total fixtures)"
  elif [[ $cpp_dag_total -eq 0 ]]; then
    pass "render-dag parity Go vs C++ (no fixtures found, skipped)"
  fi
fi

[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
