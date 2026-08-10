#!/usr/bin/env bash
# Environment preparation for the agentic PR-review and llmdoc-update workflows.
#
# The reviewer runs on a bare ubuntu-latest with only the preinstalled
# toolchain, which left it unable to run this repo's own checks. Review of
# PR #194 reported exactly three gaps: no `ruff`, no `golangci-lint`, and a
# JDK too old for the project's target release 25. Those checks are the ones most
# likely to catch a real defect, so the reviewer reading code without them is
# a real loss of review power.
#
# Wired in via the upstream `setup_script` input (agentic-workflow-template
# PR #30). Contract we must hold up our end of:
#
#   - Read from a trusted source. `pr-review` reads this file from the PR's
#     base commit, so the PR under review cannot rewrite it. That blocks one
#     class of injection and is NOT a security boundary: `mvn` still executes
#     the head worktree's pom.xml. The real boundary is the job's read-only
#     token plus not handing GitHub/PAT credentials to the agent.
#   - Leave the worktree clean. Everything installed here lands outside the
#     repo or under an already-gitignored path; the upstream hook diffs
#     worktree state before and after and fails the job on anything new.
#   - Nonzero exit is not fatal. The agent is told preparation is incomplete
#     and discloses which checks it could not run, so partial success is
#     worth more than aborting. Hence `set +e` semantics per phase below
#     rather than `set -e` over the whole script.
#
# Budget: the upstream hook caps us at 13 minutes (SIGTERM, then SIGKILL 30s
# later) with a step-level 15-minute backstop. Phases are ordered cheapest
# first and each is individually time-boxed, so a slow phase costs only its
# own capability rather than everything after it. pine-cpp is last precisely
# because it is the expensive one.

set -uo pipefail

# Use the cwd, NOT this file's location. The hook invokes us as
# `cd "$repo_dir" && bash "$hook"`, and $hook lives in .trusted-base — a sparse
# checkout holding only this script. Deriving the root from BASH_SOURCE would
# point every phase at that near-empty directory, so each one would fail to
# find pine-go/go.mod, pine-cpp/ or scripts/ and setup would report every
# capability unavailable while having installed nothing.
REPO_ROOT="$PWD"

# Assert we are actually at a repo root rather than trusting the caller. If
# this is wrong, failing here with one clear message beats five phases each
# failing for its own confusing reason.
for marker in pine-go/go.mod pine-cpp/CMakeLists.txt scripts/ci-apt-install.sh; do
  if [[ ! -e "$REPO_ROOT/$marker" ]]; then
    echo "::error::setup must run from the repository root; $marker not found in $REPO_ROOT"
    exit 1
  fi
done

# Installed tools go here, outside the repo, so nothing can dirty the worktree.
TOOLS_DIR="${RUNNER_TEMP:-/tmp}/pineapple-setup-tools"
mkdir -p "$TOOLS_DIR/bin"

# Pinned to what CI actually resolves today, verified against the run for
# PR #194 rather than copied from the config: golangci/golangci-lint-action@v9
# installed v2.12.2, and actions/setup-python@v6 with "3.13" resolved to
# CPython 3.13.14. Bump these together with .github/workflows/ci.yml.
GOLANGCI_VERSION="2.12.2"
GOLANGCI_SHA256="8df580d2670fed8fa984aac0507099af8df275e665215f5c7a2ae3943893a553"

failed=()

# Emit to GITHUB_PATH/GITHUB_ENV when present so the agent's later steps see
# the tools; fall back to a plain export for local runs of this script.
add_path() {
  echo "$1" >> "${GITHUB_PATH:-/dev/null}"
  export PATH="$1:$PATH"
}

add_env() {
  echo "$1=$2" >> "${GITHUB_ENV:-/dev/null}"
  export "$1"="$2"
}

# Overall deadline, kept a little under the hook's own cap so this script always
# gets to print its summary.
#
# This matters because the per-phase budgets deliberately sum to more than the
# hook allows (990s vs a 780s default): they are ceilings for a pathological
# phase, not expected runtimes. But if the total were ever actually reached, the
# hook would kill this process group and the "unavailable: X, Y" summary at the
# bottom would never run — and that summary is the agent's only signal about
# which capability is missing. So track a deadline ourselves and skip remaining
# phases when the budget is spent, which keeps the summary on every path.
#
# SETUP_HOOK_TIMEOUT mirrors the upstream variable; parse the same m/s suffixes
# it accepts so an overridden hook timeout is respected instead of assumed.
# Accept only a whole string of digits with an optional single m/s suffix, and
# fall back to the documented default for anything else. Matching on a suffix
# first is not enough: forms like "13m30s", "1e3s" and "0.5m" all end in a
# plausible unit, and feeding them to arithmetic either aborts the script under
# `set -u` or yields a negative deadline that silently skips every phase — which
# looks exactly like an honestly constrained runner. GNU timeout also accepts
# h/d, but this budget is minutes-scale, so treat those as unsupported rather
# than pretending to honour them.
# Always emit a base-10-normalized integer. `+([0-9])` accepts leading zeros and
# bash arithmetic then reads them as octal, which failed two different ways.
# "010m" quietly meant 480s instead of 600s. "0900s" was worse: the `*s` branch
# does no arithmetic, so the raw string escaped to the caller and blew up at the
# DEADLINE assignment, leaving DEADLINE unset — `set -u` then aborted the script
# before any phase ran and before the no-budget guard below could report
# anything, which is the one outcome this machinery exists to prevent. ("08m" by
# contrast failed inside the function, so the guard did report it, just with the
# wrong reason.) `10#` forces decimal and the arithmetic result, never the raw
# string, is what gets returned.
hook_budget_seconds() {
  local v="${SETUP_HOOK_TIMEOUT:-13m}" n
  # Strip at most one trailing unit, then require what remains to be all
  # digits. Checking the unit alone is not sufficient: "13m30s" and "1e3s" both
  # end in a valid unit yet leave a non-numeric remainder.
  case "$v" in
    # Cap the digit count before doing arithmetic. `10#` pins the radix but not
    # the magnitude, and a long enough value overflows into a large positive
    # number, so the script would think it had ample budget and trim nothing —
    # the very outcome this machinery is meant to prevent. (Overflow to a
    # negative value is caught by the DEADLINE guard below, but not this one.)
    *m) n=${v%m}; [[ "$n" == +([0-9]) && ${#n} -le 6 ]] && { echo $(( 10#$n * 60 )); return; } ;;
    *s) n=${v%s}; [[ "$n" == +([0-9]) && ${#n} -le 8 ]] && { echo $(( 10#$n )); return; } ;;
    *)  [[ "$v" == +([0-9]) && ${#v} -le 8 ]] && { echo $(( 10#$v )); return; } ;;
  esac
  echo 780
}
START_TS=$SECONDS
# Leave headroom for the summary itself and for the final phase's own kill-after.
DEADLINE=$(( $(hook_budget_seconds) - 45 ))
# A budget too small to attempt anything would otherwise skip every phase and
# read as a constrained runner. Say so instead.
if [[ $DEADLINE -le 10 ]]; then
  echo "::warning::SETUP_HOOK_TIMEOUT=${SETUP_HOOK_TIMEOUT:-13m} leaves no usable budget (deadline=${DEADLINE}s after reserving headroom); skipping all preparation"
  echo "Unavailable capabilities: all phases (no usable time budget)" >&2
  exit 1
fi

# Each phase gets its own wall-clock cap. A phase that overruns is killed and
# recorded as failed; the phases after it still get their full budget. Without
# this, one slow mirror would consume the whole 13 minutes and leave the
# reviewer with nothing, which is the outcome the upstream design is trying
# to avoid.
#
# The phase body runs in a subshell because `timeout` is a separate binary and
# cannot invoke a shell function directly; hence the `export -f` below. Losing
# the subshell's own `export PATH` is fine: no phase depends on a previous
# phase's PATH, and what the agent actually reads are the GITHUB_PATH and
# GITHUB_ENV appends, which are file writes and survive the subshell.
phase() {
  local name=$1 budget=$2 fn=$3
  local elapsed=$(( SECONDS - START_TS ))
  local left=$(( DEADLINE - elapsed ))
  if [[ $left -le 10 ]]; then
    echo "::warning::setup phase '$name' skipped: overall budget spent (${elapsed}s)"
    failed+=("$name (no time left)")
    return 1
  fi
  # Never let a phase run past the overall deadline, even if its own ceiling
  # is larger than the time remaining.
  local secs=${budget%s}
  if [[ $secs -gt $left ]]; then
    echo "::notice::setup phase '$name' budget trimmed ${budget} -> ${left}s (overall deadline)"
    budget="${left}s"
  fi
  echo "::group::setup: $name (budget ${budget})"
  local rc=0
  timeout --signal=TERM --kill-after=20s "$budget" \
    bash -c "set -uo pipefail; $fn" || rc=$?
  echo "::endgroup::"
  if [[ $rc -ne 0 ]]; then
    # 124 is the ordinary timeout: TERM was delivered and the phase stopped.
    # Measured, not assumed — a phase that forks a tree of children (cmake's
    # compilers) still reports 124. Note that 124 says the direct child was
    # signalled, not that every descendant died: a TERM-ignoring grandchild can
    # outlive the phase and still yield 124.
    #
    # 137 means TERM did not do the job: either the phase blocked/ignored it and
    # --kill-after escalated to KILL, or something outside killed us, which on a
    # runner is usually the OOM killer. Those are different problems, and 137
    # cannot tell them apart, so report it as its own category rather than
    # filing it under either "out of budget" or "broken".
    if [[ $rc -eq 124 ]]; then
      echo "::warning::setup phase '$name' exceeded ${budget}, skipping"
    elif [[ $rc -eq 137 ]]; then
      echo "::warning::setup phase '$name' was SIGKILLed (rc=137): budget escalation or an external kill such as OOM"
    else
      echo "::warning::setup phase '$name' failed (rc=${rc})"
    fi
    failed+=("$name")
    return 1
  fi
  echo "==> $name ready"
  return 0
}

# --- JDK 25 ------------------------------------------------------------------
# pom.xml sets maven.compiler.source/target to 25, and the runner's default JDK is 17,
# which is what made the reviewer report "JDK does not support the required
# target release". No download needed: the runner image ships Temurin 25 in the
# toolcache and exposes it as JAVA_HOME_25_X64.
setup_java() {
  local home="${JAVA_HOME_25_X64:-}"
  if [[ -z "$home" ]]; then
    # Fall back to a toolcache scan; the env var is an image convention, not a
    # documented contract, so do not treat its absence as fatal on its own.
    # shellcheck disable=SC2012  # fixed toolcache layout, no exotic filenames
    home=$(ls -d /opt/hostedtoolcache/Java_Temurin-Hotspot_jdk/25.*/x64 2>/dev/null | sort -V | tail -1)
  fi
  if [[ -z "$home" || ! -x "$home/bin/javac" ]]; then
    echo "no JDK 25 found in the runner toolcache" >&2
    return 1
  fi
  add_env JAVA_HOME "$home"
  add_path "$home/bin"
  "$home/bin/javac" -version
}

# --- Go ----------------------------------------------------------------------
# pine-go/go.mod requires go 1.26.2. The runner's PATH Go is the toolcache
# default (1.24 line), so an unqualified `go build` fails on the language
# version. Pick the newest 1.26.x the toolcache has.
setup_go() {
  local want_minor go_bin
  # Patch level is optional in go.mod (`go 1.26` is as valid as `go 1.26.2`),
  # so do not require the second dot.
  want_minor=$(sed -nE 's/^go 1\.([0-9]+).*/\1/p' pine-go/go.mod | head -1)
  if [[ -z "$want_minor" ]]; then
    echo "could not read the Go minor version from pine-go/go.mod" >&2
    return 1
  fi
  # shellcheck disable=SC2012  # fixed toolcache layout, no exotic filenames
  go_bin=$(ls -d "/opt/hostedtoolcache/go/1.${want_minor}."*/x64/bin 2>/dev/null | sort -V | tail -1)
  if [[ -n "$go_bin" && -x "$go_bin/go" ]]; then
    add_path "$go_bin"
  elif ! command -v go >/dev/null 2>&1; then
    echo "no Go 1.${want_minor}.x in the toolcache and none on PATH" >&2
    return 1
  else
    # Whatever is on PATH may still be too old; report it and let the
    # reviewer's own build output speak, rather than failing preparation.
    echo "::warning::no Go 1.${want_minor}.x in the toolcache, using $(go version)"
  fi
  go version
}

# --- Python tooling ----------------------------------------------------------
# A venv outside the repo, so nothing lands in the worktree and the runner's
# externally-managed system Python is left alone. Versions are unpinned to
# match CI, which also installs these unpinned.
setup_python() {
  local venv="$TOOLS_DIR/venv"
  python3 -m venv "$venv" || return 1
  "$venv/bin/pip" install --quiet --disable-pip-version-check ruff pytest pytest-cov || return 1
  add_path "$venv/bin"
  "$venv/bin/ruff" --version
}

# --- golangci-lint -----------------------------------------------------------
# CI gets this from golangci/golangci-lint-action; here we fetch the same
# version directly and verify it against the published checksum, since this
# script runs with the worktree already checked out.
setup_golangci() {
  local url tarball
  url="https://github.com/golangci/golangci-lint/releases/download/v${GOLANGCI_VERSION}/golangci-lint-${GOLANGCI_VERSION}-linux-amd64.tar.gz"
  tarball="$TOOLS_DIR/golangci-lint.tar.gz"
  curl -sSfL --retry 3 --retry-delay 2 -o "$tarball" "$url" || return 1
  echo "${GOLANGCI_SHA256}  ${tarball}" | sha256sum -c - || return 1
  tar -xzf "$tarball" -C "$TOOLS_DIR" \
    --strip-components=1 \
    "golangci-lint-${GOLANGCI_VERSION}-linux-amd64/golangci-lint" || return 1
  mv "$TOOLS_DIR/golangci-lint" "$TOOLS_DIR/bin/golangci-lint" || return 1
  add_path "$TOOLS_DIR/bin"
  "$TOOLS_DIR/bin/golangci-lint" --version
}

# --- pine-cpp ----------------------------------------------------------------
# Last, and with the largest budget, because it is the only phase that has ever
# been the reason a job failed on infrastructure rather than on code: apt broke
# CI twice on slow-mirror days (#125, #164). ci-apt-install.sh retries with
# mirror rotation. If this phase is dropped, every earlier phase still stands.
#
# Build dir is pine-cpp/build-tests, matching scripts/cpp-test.sh so the
# reviewer's `make cpp-test` reuses this cache instead of configuring again.
# The path is covered by .gitignore's `pine-cpp/build*/`, as is CMake's
# FetchContent cache underneath it, so the worktree stays clean.
setup_cpp() {
  # Bound apt as a whole, not just per attempt, and size the bound from every
  # term in the retry loop rather than from the obvious one.
  #
  # ci-apt-install.sh runs its loop twice, for update and for install. Per loop,
  # worst case, with ATTEMPTS=3 and ATTEMPT_TIMEOUT=40:
  #
  #   3 attempts x (40s + 10s kill-after)      = 150s
  #   2 inter-attempt gaps x (30s + 5s dpkg
  #     repair + ~2s rotate)                   =  74s
  #   backoff sleeps of 10s and 20s            =  30s
  #                                             ----
  #   one loop                                  254s   ->  both loops 508s
  #
  # Hence the 510s bound below. Two earlier versions of this comment were wrong
  # in the same direction, and both times the number looked like arithmetic: the
  # first computed the worst case from the script's default parameters instead of
  # the ones passed here, the second counted only attempts and backoffs and left
  # out the dpkg repair (then unbounded) and the rotation. Every term inside the
  # loop belongs in the sum, including the ones that only run between attempts.
  #
  # The phase ceiling is 600s and cmake needs ~44s locally (9s to configure,
  # including cloning ~70 MB of rapidjson + doctest, plus 35s to build the test
  # target at -j4), so 510s for apt still leaves headroom on a slower runner.
  # 3 attempts is kept deliberately: it preserves two mirror rotations, which is
  # the part that addresses the "mirror answers but crawls" mode from #164.
  timeout --signal=TERM --kill-after=15s 510s \
    env ATTEMPTS=3 ATTEMPT_TIMEOUT=40 \
    bash scripts/ci-apt-install.sh libluajit-5.1-dev libcurl4-openssl-dev || return 1
  # Assert rather than merely print: `set -e` is deliberately off here, so an
  # unguarded `cmake --version` would emit "command not found" and carry on to
  # fail less clearly at the configure step. Per the convention in
  # ci-apt-install.sh, a missing preinstalled tool must fail at this point.
  command -v cmake >/dev/null || { echo "cmake not found on PATH" >&2; return 1; }
  cmake --version | head -1
  cmake -S pine-cpp -B pine-cpp/build-tests \
    -DCMAKE_BUILD_TYPE=Debug \
    -DPINE_CPP_BUILD_TESTS=ON \
    -DCMAKE_POLICY_VERSION_MINIMUM=3.5 || return 1
  # Cap the job count. The runner has 4 cores so nproc alone would be fine
  # there, but this script is also runnable locally, where a bare -j is a
  # standing rule violation in this repo (it swap-storms a big dev box).
  # Validate rather than trust: if nproc is missing or prints something
  # unexpected, `jobs` would be empty and -j"$jobs" would expand to a bare -j,
  # committing the very violation this block cites.
  local jobs
  jobs=$(nproc 2>/dev/null)
  [[ "$jobs" == +([0-9]) ]] || jobs=4
  [[ $jobs -gt 12 ]] && jobs=12
  cmake --build pine-cpp/build-tests --target pine_cpp_tests -j"$jobs" || return 1
}

# Exported so the `bash -c` inside phase() can see them. Without this every
# phase dies with rc=127 and setup reports every capability unavailable while
# having done nothing — a failure mode that reading the script does not show.
export -f setup_java setup_go setup_python setup_golangci setup_cpp
export -f add_path add_env
export TOOLS_DIR GOLANGCI_VERSION GOLANGCI_SHA256

# Ordered cheapest first. Budgets sum to more than the hook's 13 minutes on
# purpose: they are per-phase ceilings for a pathological phase, not expected
# runtimes. phase() enforces the overall deadline on top, trimming or skipping
# so that the summary below is reached on every path.
phase "JDK 25"        30s  setup_java
phase "Go toolchain"  60s  setup_go
phase "Python tools"  180s setup_python
phase "golangci-lint" 120s setup_golangci
phase "pine-cpp"      600s setup_cpp

if [[ ${#failed[@]} -gt 0 ]]; then
  # Exit nonzero so the upstream hook tells the agent preparation was partial
  # and it should disclose which checks it could not run. Naming the phases
  # here matters: that message is the agent's only signal about which
  # capability is missing, and a bare exit code would leave it guessing.
  #
  # Join with "; " rather than the default space: phase names contain spaces
  # ("JDK 25", "Go toolchain"), so a space-joined list has no recoverable item
  # boundaries for a reader trying to enumerate what is missing. `${arr[*]}`
  # cannot do this — IFS contributes only its first character — so build the
  # string explicitly.
  joined=$(printf '%s; ' "${failed[@]}")
  joined=${joined%; }
  echo "::warning::setup incomplete, unavailable: ${joined}"
  echo "Unavailable capabilities: ${joined}" >&2
  exit 1
fi

echo "==> setup complete: all phases ready"
