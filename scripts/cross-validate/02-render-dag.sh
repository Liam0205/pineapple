#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 2. Render-DAG parity ----------
echo
echo "==> [2/$TOTAL_SECTIONS] Render-DAG parity"

dag_pass=0
dag_total=0
py_dag_pass=0
py_dag_total=0

for fixture in "$REPO_ROOT"/fixtures/pipelines/*.json; do
  [[ -f "$fixture" ]] || continue
  [[ "$fixture" == *.go ]] && continue
  fname=$(basename "$fixture")

  dag_total=$((dag_total + 1))

  # Extract .config from fixture to a temp file
  config_file="$WORK_DIR/dag_config_${fname}"
  python3 -c "
import json
with open('$fixture') as f:
    data = json.load(f)
cfg = data.get('config', data)
with open('$config_file', 'w') as cf:
    json.dump(cfg, cf)
" || { fail "render-dag extract config: $fname"; continue; }

  go_dot=$("$WORK_DIR/pineapple-dag" -config "$config_file" -format dot 2>/dev/null) || {
    fail "render-dag Go failed: $fname"; continue
  }

  java_dot=$(java_run page.liam.pine.RenderDAGCli -config "$config_file" -format dot 2>/dev/null) || {
    fail "render-dag Java failed: $fname"; continue
  }

  if [[ "$go_dot" == "$java_dot" ]]; then
    dag_pass=$((dag_pass + 1))
    echo "    [$dag_total] $fname (dot) Go vs Java → match"
  else
    fail "render-dag divergence (Go vs Java): $fname (dot)"
    diff <(echo "$go_dot") <(echo "$java_dot") >&2 || true
  fi

  # Go vs Python DOT
  py_dag_total=$((py_dag_total + 1))
  py_dot=$(py_run pine.cli.dag -config "$config_file" -format dot 2>/dev/null) || {
    fail "render-dag Python dot failed: $fname"
    py_dot=""
  }

  if [[ -n "$py_dot" ]]; then
    if [[ "$go_dot" == "$py_dot" ]]; then
      py_dag_pass=$((py_dag_pass + 1))
      echo "    [$dag_total] $fname (dot) Go vs Python → match"
    else
      fail "render-dag divergence (Go vs Python): $fname (dot)"
      diff <(echo "$go_dot") <(echo "$py_dot") >&2 || true
    fi
  fi

  # Mermaid format parity
  dag_total=$((dag_total + 1))

  go_mmd=$("$WORK_DIR/pineapple-dag" -config "$config_file" -format mermaid 2>/dev/null) || {
    fail "render-dag Go mermaid failed: $fname"; continue
  }

  java_mmd=$(java_run page.liam.pine.RenderDAGCli -config "$config_file" -format mermaid 2>/dev/null) || {
    fail "render-dag Java mermaid failed: $fname"; continue
  }

  if [[ "$go_mmd" == "$java_mmd" ]]; then
    dag_pass=$((dag_pass + 1))
    echo "    [$dag_total] $fname (mermaid) Go vs Java → match"
  else
    fail "render-dag divergence (Go vs Java): $fname (mermaid)"
    diff <(echo "$go_mmd") <(echo "$java_mmd") >&2 || true
  fi

  # Go vs Python Mermaid
  py_dag_total=$((py_dag_total + 1))
  py_mmd=$(py_run pine.cli.dag -config "$config_file" -format mermaid 2>/dev/null) || {
    fail "render-dag Python mermaid failed: $fname"
    py_mmd=""
  }

  if [[ -n "$py_mmd" ]]; then
    if [[ "$go_mmd" == "$py_mmd" ]]; then
      py_dag_pass=$((py_dag_pass + 1))
      echo "    [$dag_total] $fname (mermaid) Go vs Python → match"
    else
      fail "render-dag divergence (Go vs Python): $fname (mermaid)"
      diff <(echo "$go_mmd") <(echo "$py_mmd") >&2 || true
    fi
  fi

  # Test collapsed DAG if fixture has non-empty pipeline_map
  if grep -q '"pipeline_map"' "$fixture" 2>/dev/null; then
    for collapse_level in 1 2; do
      for cfmt in dot mermaid; do
        dag_total=$((dag_total + 1))

        go_col=$("$WORK_DIR/pineapple-dag" -config "$config_file" -format "$cfmt" -collapse "$collapse_level" 2>/dev/null) || {
          fail "render-dag Go $cfmt collapsed=$collapse_level failed: $fname"; continue
        }

        java_col=$(java_run page.liam.pine.RenderDAGCli -config "$config_file" -format "$cfmt" -collapse "$collapse_level" 2>/dev/null) || {
          fail "render-dag Java $cfmt collapsed=$collapse_level failed: $fname"; continue
        }

        if [[ "$go_col" == "$java_col" ]]; then
          dag_pass=$((dag_pass + 1))
          echo "    [$dag_total] $fname ($cfmt collapse=$collapse_level) Go vs Java → match"
        else
          fail "render-dag divergence (Go vs Java): $fname ($cfmt collapse=$collapse_level)"
          diff <(echo "$go_col") <(echo "$java_col") >&2 || true
        fi

        # Go vs Python collapsed
        py_dag_total=$((py_dag_total + 1))
        py_col=$(py_run pine.cli.dag -config "$config_file" -format "$cfmt" -collapse "$collapse_level" 2>/dev/null) || {
          fail "render-dag Python $cfmt collapsed=$collapse_level failed: $fname"
          py_col=""
        }

        if [[ -n "$py_col" ]]; then
          if [[ "$go_col" == "$py_col" ]]; then
            py_dag_pass=$((py_dag_pass + 1))
            echo "    [$dag_total] $fname ($cfmt collapse=$collapse_level) Go vs Python → match"
          else
            fail "render-dag divergence (Go vs Python): $fname ($cfmt collapse=$collapse_level)"
            diff <(echo "$go_col") <(echo "$py_col") >&2 || true
          fi
        fi
      done
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

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
