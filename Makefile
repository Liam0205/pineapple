# Pineapple top-level Makefile — the single entry point for repo-wide tasks.
#
# Design notes:
#   - Polyglot (apple Python / pine-go Go / pine-java Java / pine-cpp C++).
#     Top-level targets delegate to scripts/*.sh or to each subdirectory's build
#     system (go / mvn / cmake).
#   - No inline multi-step recipes: the actual steps live in scripts/*.sh; the
#     Makefile only names and composes them.
#   - Target names match the wangshu Makefile (fmt/lint/test/bench/fuzz/cover/tidy/hooks/all)
#     so one muscle memory works for both; language-specific targets use <lang>-<verb>.
#   - `all` shares the name but is deliberately narrower: wangshu's `all` is
#     `fmt lint test fuzz conformance difftest bench-test` (a full check for a
#     single language); ours is `fmt-check lint test codegen-check`. The slow
#     cross-language jobs (fuzz / cross-validate / differential-fuzz / the cpp
#     targets) are explicit targets so a local `make all` does not block for 1h+.
#     Full check: `make all && make fuzz && make cross-validate && make differential-fuzz`.

# Needed for bash syntax (process substitution / arrays); the default /bin/sh fails with a syntax error.
SHELL := /bin/bash

# Default -j parallelism (the dev machine has 12 cores). CI overrides it with
# `make <target> PARALLEL=$(nproc)`. Never pass a bare -j to cmake/make: without
# an upper bound it triggers an OOM swap storm.
PARALLEL ?= 12

.PHONY: help all \
        fmt fmt-check \
        lint \
        test go-test apple-test java-test cpp-test \
        bench go-bench java-bench bench-cross-runtime bench-lua-backends \
        fuzz go-fuzz java-fuzz differential-fuzz \
        cover go-cover java-cover \
        codegen codegen-check \
        cross-validate \
        hooks tidy clean \
        bump tag-release check-pr-ci

# Default target: print help, so a bare `make` never does anything by accident.
help:
	@awk 'BEGIN {FS=":.*##"; printf "Pineapple targets (run \033[36mmake <target>\033[0m):\n\n"} \
	     /^[a-zA-Z_-]+:.*?##/ {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ------- Composite targets ----------------------------------------------------

all: fmt-check lint test codegen-check ## Full pre-commit check (excludes slow cross-language jobs: cross-validate / differential-fuzz / fuzz)

# ------- Formatting -----------------------------------------------------------

fmt: ## Format every language in place (gofmt / clang-format / ruff format / mvn)
	@cd pine-go && gofmt -w $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './vendor/*')
	@if command -v clang-format >/dev/null 2>&1; then \
	    find pine-cpp -type d \( -name 'build' -o -name 'build-*' \) -prune \
	        -o -type f \( -name '*.cpp' -o -name '*.hpp' \) -print0 \
	      | xargs -0 -r clang-format -i; \
	  fi
	@if command -v ruff >/dev/null 2>&1; then ruff format apple/; fi

fmt-check: ## Format dry-run (for CI; any diff fails)
	@out=$$(cd pine-go && gofmt -l $$(git ls-files '*.go')); \
	  if [ -n "$$out" ]; then echo "gofmt diff in:"; echo "$$out"; exit 1; fi
	@find pine-cpp -type d \( -name 'build' -o -name 'build-*' \) -prune \
	    -o -type f \( -name '*.cpp' -o -name '*.hpp' \) -print0 \
	  | xargs -0 -r clang-format --dry-run --Werror

# ------- Lint -----------------------------------------------------------------

lint: ## Lint every language (ruff / golangci-lint / checkstyle / clang-format)
	bash scripts/lint.sh

# ------- Test -----------------------------------------------------------------

test: ## go + apple + java tests (test-all.sh)
	bash scripts/test-all.sh

go-test: ## pine-go tests only
	bash scripts/go-test.sh

apple-test: ## apple Python tests only
	@if [ -f .venv/bin/activate ]; then . .venv/bin/activate; fi; \
	python3 -m pytest apple/tests/ -v

java-test: ## pine-java tests only
	bash scripts/java-test.sh

cpp-test: ## pine-cpp tests only (CMake + ctest; parallelism set by PARALLEL)
	bash scripts/cpp-test.sh PARALLEL=$(PARALLEL)

# ------- Coverage -------------------------------------------------------------

cover: go-cover java-cover ## Coverage reports for every language

go-cover: ## pine-go coverprofile + per-func summary in the terminal
	cd pine-go && go test -coverprofile=coverage.out -covermode=atomic ./...
	cd pine-go && go tool cover -func=coverage.out | tail -1

java-cover: ## pine-java Jacoco report
	cd pine-java && mvn test -B -q jacoco:report

# ------- Benchmark ------------------------------------------------------------

bench: go-bench java-bench ## Per-language benchmarks (run serially; use bench-cross-runtime for cross-engine)

go-bench: ## pine-go go test -bench
	bash scripts/go-bench.sh

java-bench: ## pine-java fixture benchmark
	bash scripts/java-bench.sh

bench-cross-runtime: ## Cross-engine benchmark (three engines × many fixtures; needs hey + Go/Java/C++ toolchains)
	bash scripts/bench-cross-runtime.sh

# wangshu vs gopher-lua backend benchmark: runs both backends back to back on the
# same machine, then compares them with benchstat. The reference fixture is
# realistic_for_you_calibrated (see benchmark-hygiene.md).
# Requires benchstat: go install golang.org/x/perf/cmd/benchstat@latest
bench-lua-backends: ## wangshu vs gopher-lua on realistic_*_calibrated(benchstat delta)
	bash scripts/bench-lua-backends.sh

# ------- Fuzz -----------------------------------------------------------------

fuzz: go-fuzz java-fuzz ## 30s fuzz smoke run for every language

go-fuzz: ## pine-go: discover every func Fuzz* and run each for 30s
	bash scripts/go-fuzz.sh 30s

java-fuzz: ## pine-java Jazzer fuzz 60s
	bash scripts/java-fuzz.sh 60

differential-fuzz: ## Cross-engine differential fuzz (default 1000 rounds, Go vs Java)
	bash scripts/differential-fuzz.sh

# ------- Codegen --------------------------------------------------------------

codegen: ## Generate apple_generated/ + doc/operators/ from the pine-go Registry
	bash scripts/codegen.sh

codegen-check: codegen ## For CI: git diff --exit-code after codegen (generated files must be up to date)
	git diff --exit-code apple_generated/ doc/operators/

# ------- Cross-validate -------------------------------------------------------

cross-validate: ## Cross-engine parity checks (parallel; sections live in scripts/cross-validate/)
	bash scripts/cross-validate.sh

# ------- Tooling --------------------------------------------------------------

hooks: ## Install git hooks (one-off; same as git config core.hooksPath .githooks)
	git config core.hooksPath .githooks
	@echo "hooks installed: $$(git config core.hooksPath)"

tidy: ## go mod tidy (main module + benchmarks submodule) + git diff check (pine-java/pine-cpp are managed by mvn/CMake and have no equivalent)
	cd pine-go && go mod tidy
	cd pine-go/benchmarks && go mod tidy
	git diff --exit-code pine-go/go.mod pine-go/go.sum pine-go/benchmarks/go.mod pine-go/benchmarks/go.sum

clean: ## Remove pine-go / pine-cpp / pine-java build outputs
	cd pine-go && go clean -testcache -cache
	rm -rf pine-cpp/build pine-cpp/build-*
	cd pine-java && mvn -B -q clean

# ------- Release / diagnostics ------------------------------------------------

bump: ## Sync the version number across 5 places + full verification (usage: make bump VERSION=0.10.0)
ifndef VERSION
	$(error VERSION not set; usage: make bump VERSION=0.10.0)
endif
	bash scripts/bump-version.sh $(VERSION)

tag-release: ## Check the 4 version numbers agree + create and push both vX.Y.Z and pine-go/vX.Y.Z tags
	bash scripts/tag-release.sh

check-pr-ci: ## Block on the current PR's CI (and report review activity / unresolved threads); .githooks/pre-push already calls this, so this target is only a manual diagnostic entry point
	bash scripts/check-pr-ci.sh
