#!/usr/bin/env bash
# Parallel execution helpers for cross-validate sections.
# Source this file to get access to parallel fixture processing.

# Max parallel jobs (default: nproc or 8, whichever is smaller)
: "${CV_JOBS:=$(( $(nproc 2>/dev/null || echo 4) ))}"
[[ $CV_JOBS -gt 16 ]] && CV_JOBS=16

# run_engines_parallel config request out_prefix [res_args...]
# Runs Go, Java, Python (and optionally C++) on the same config+request in parallel.
# Results are written to $out_prefix.{go,java,py,cpp}.{out,err,rc}
run_engines_parallel() {
  local config="$1" request="$2" out_prefix="$3"
  shift 3
  local res_args=("$@")

  # Use `|| _rc=$?` to prevent set -e from killing subshells on non-zero exit
  { _rc=0; "$WORK_DIR/pineapple-run" -config "$config" -request "$request" "${res_args[@]}" \
      > "${out_prefix}.go.out" 2>"${out_prefix}.go.err" || _rc=$?; echo "$_rc" > "${out_prefix}.go.rc"; } &
  local go_pid=$!

  { _rc=0; java_run page.liam.pine.RunCli -config "$config" -request "$request" "${res_args[@]}" \
      > "${out_prefix}.java.out" 2>"${out_prefix}.java.err" || _rc=$?; echo "$_rc" > "${out_prefix}.java.rc"; } &
  local java_pid=$!

  { _rc=0; cd "$REPO_ROOT/pine-python" && python3 -m pine.cli.run -config "$config" -request "$request" "${res_args[@]}" \
      > "${out_prefix}.py.out" 2>"${out_prefix}.py.err" || _rc=$?; echo "$_rc" > "${out_prefix}.py.rc"; } &
  local py_pid=$!

  local cpp_pid=""
  if [[ -n "${CPP_RUN:-}" ]]; then
    { _rc=0; "$CPP_RUN" -config "$config" -request "$request" "${res_args[@]}" \
        > "${out_prefix}.cpp.out" 2>"${out_prefix}.cpp.err" || _rc=$?; echo "$_rc" > "${out_prefix}.cpp.rc"; } &
    cpp_pid=$!
  fi

  wait $go_pid $java_pid $py_pid ${cpp_pid:+$cpp_pid} 2>/dev/null || true
}

# job_pool_wait — wait for background jobs to drop below CV_JOBS
job_pool_wait() {
  while [[ $(jobs -rp | wc -l) -ge $CV_JOBS ]]; do
    sleep 0.05
  done
}
