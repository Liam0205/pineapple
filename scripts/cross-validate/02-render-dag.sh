#!/usr/bin/env bash
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/_env.sh"

# ---------- 2. Render-DAG parity ----------
echo
echo "==> [2/$TOTAL_SECTIONS] Render-DAG parity"

dag_pass=0
dag_total=0

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
    echo "    [$dag_total] $fname (dot) → match"
  else
    fail "render-dag divergence: $fname (dot)"
    diff <(echo "$go_dot") <(echo "$java_dot") >&2 || true
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
    echo "    [$dag_total] $fname (mermaid) → match"
  else
    fail "render-dag divergence: $fname (mermaid)"
    diff <(echo "$go_mmd") <(echo "$java_mmd") >&2 || true
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
          echo "    [$dag_total] $fname ($cfmt collapse=$collapse_level) → match"
        else
          fail "render-dag divergence: $fname ($cfmt collapse=$collapse_level)"
          diff <(echo "$go_col") <(echo "$java_col") >&2 || true
        fi
      done
    done
  fi
done

if [[ $dag_total -gt 0 && $dag_pass -eq $dag_total ]]; then
  pass "render-dag parity ($dag_pass/$dag_total fixtures)"
elif [[ $dag_total -eq 0 ]]; then
  pass "render-dag parity (no fixtures found, skipped)"
fi

# Return to caller if sourced, exit if run directly
[[ "${BASH_SOURCE[0]}" == "${0}" ]] && exit $_CV_FAIL || true
