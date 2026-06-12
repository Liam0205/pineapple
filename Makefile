# Pineapple 顶层 Makefile —— 全仓任务的统一入口。
#
# 设计要点:
#   - 多语言异构(apple Python / pine-go Go / pine-java Java / pine-cpp C++)。
#     顶层 target 透传到 scripts/*.sh 或子目录构建系统(go / mvn / cmake)。
#   - 不内联多步命令: 所有具体步骤仍位于 scripts/*.sh,Makefile 只做命名与组合。
#   - target 命名与 wangshu Makefile 对齐(fmt/lint/test/bench/fuzz/cover/tidy/hooks/all),
#     避免双 muscle memory; 多语言专属 target 走 <lang>-<verb> 形式。

# bash 语法(process substitution / array)需要,默认 /bin/sh 会报 syntax error。
SHELL := /bin/bash

.PHONY: help all \
        fmt fmt-check \
        lint \
        test go-test apple-test java-test cpp-test \
        bench go-bench java-bench bench-cross-runtime bench-lua-backends \
        fuzz go-fuzz java-fuzz differential-fuzz \
        cover \
        codegen codegen-check \
        cross-validate \
        hooks tidy clean

# 默认 target: 输出帮助。"裸跑 make"不会触发任何动作,避免误操作。
help:
	@awk 'BEGIN {FS=":.*##"; printf "Pineapple targets (run \033[36mmake <target>\033[0m):\n\n"} \
	     /^[a-zA-Z_-]+:.*?##/ {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# ------- 复合 target ----------------------------------------------------------

all: lint test codegen-check ## 本地提交前全检(不含跨语言慢 job:cross-validate / differential-fuzz)

# ------- 格式化 ---------------------------------------------------------------

fmt: ## 各语言格式化(写回; gofmt / clang-format / ruff format / mvn)
	@cd pine-go && gofmt -w $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './vendor/*')
	@if command -v clang-format >/dev/null 2>&1; then \
	    find pine-cpp -type d \( -name 'build' -o -name 'build-*' \) -prune \
	        -o -type f \( -name '*.cpp' -o -name '*.hpp' \) -print0 \
	      | xargs -0 -r clang-format -i; \
	  fi
	@if command -v ruff >/dev/null 2>&1; then ruff format apple/; fi

fmt-check: ## 格式 dry-run(CI 用; 任何 diff 即 fail)
	@out=$$(cd pine-go && gofmt -l $$(git ls-files '*.go')); \
	  if [ -n "$$out" ]; then echo "gofmt diff in:"; echo "$$out"; exit 1; fi
	@find pine-cpp -type d \( -name 'build' -o -name 'build-*' \) -prune \
	    -o -type f \( -name '*.cpp' -o -name '*.hpp' \) -print0 \
	  | xargs -0 -r clang-format --dry-run --Werror

# ------- Lint -----------------------------------------------------------------

lint: ## 全语言 lint(ruff / golangci-lint / checkstyle / clang-format)
	bash scripts/lint.sh

# ------- Test -----------------------------------------------------------------

test: ## go + apple + java 测试(test-all.sh)
	bash scripts/test-all.sh

go-test: ## 仅 pine-go 测试
	bash scripts/go-test.sh

apple-test: ## 仅 apple Python 测试
	@if [ -f .venv/bin/activate ]; then . .venv/bin/activate; fi; \
	python3 -m pytest apple/tests/ -v

java-test: ## 仅 pine-java 测试
	bash scripts/java-test.sh

cpp-test: ## 仅 pine-cpp 测试(CMake + ctest)
	cmake -S pine-cpp -B pine-cpp/build-tests \
	    -DCMAKE_BUILD_TYPE=Debug \
	    -DPINE_CPP_BUILD_TESTS=ON \
	    -DCMAKE_POLICY_VERSION_MINIMUM=3.5
	cmake --build pine-cpp/build-tests --target pine_cpp_tests -j12
	cd pine-cpp/build-tests && ctest --output-on-failure

# ------- Coverage -------------------------------------------------------------

cover: ## pine-go + pine-java 覆盖率产物
	cd pine-go && go test -coverprofile=coverage.out -covermode=atomic ./...
	cd pine-go && go tool cover -func=coverage.out | tail -1
	cd pine-java && mvn test -B -q jacoco:report

# ------- Benchmark ------------------------------------------------------------

bench: go-bench java-bench ## 各语言 benchmark(单语言串行; 跨引擎请用 bench-cross-runtime)

go-bench: ## pine-go go test -bench
	bash scripts/go-bench.sh

java-bench: ## pine-java fixture benchmark
	bash scripts/java-bench.sh

bench-cross-runtime: ## 跨引擎 benchmark(三引擎 × 多 fixture; 需 hey + Go/Java/C++ toolchain)
	bash scripts/bench-cross-runtime.sh

# wangshu vs gopher-lua 后端对比 benchmark。仅在 wangshu backend 接入后生效。
# 现阶段为占位 target, 实现待 task #5 完成后填充(参见 task #6)。
bench-lua-backends: ## [TODO] wangshu vs gopher-lua on realistic_*_calibrated
	@echo "bench-lua-backends: TODO — wangshu backend 尚未接入(见 .code-review/go-native-lua-vm/roadmap.md)"
	@exit 1

# ------- Fuzz -----------------------------------------------------------------

fuzz: go-fuzz java-fuzz ## 各语言 fuzz 30s 冒烟

go-fuzz: ## pine-go 自动发现 func Fuzz* 各跑 30s
	bash scripts/go-fuzz.sh 30s

java-fuzz: ## pine-java Jazzer fuzz 60s
	bash scripts/java-fuzz.sh 60

differential-fuzz: ## 三引擎差分 fuzz(默认 1000 轮 Go vs Java)
	bash scripts/differential-fuzz.sh

# ------- Codegen --------------------------------------------------------------

codegen: ## 从 pine-go Registry 生成 apple_generated/ + doc/operators/
	bash scripts/codegen.sh

codegen-check: codegen ## CI 用:codegen 后 git diff --exit-code(确保产物新鲜)
	git diff --exit-code apple_generated/ doc/operators/

# ------- Cross-validate -------------------------------------------------------

cross-validate: ## 19 section 跨引擎对等校验(parallel; 见 scripts/cross-validate/)
	bash scripts/cross-validate.sh

# ------- 工程基建 -------------------------------------------------------------

hooks: ## 安装 git hooks(一次性; 等价 git config core.hooksPath .githooks)
	git config core.hooksPath .githooks
	@echo "hooks installed: $$(git config core.hooksPath)"

tidy: ## go mod tidy + git diff 守门(pine-java/pine-cpp 由 mvn/CMake 自管,无对应概念)
	cd pine-go && go mod tidy
	git diff --exit-code pine-go/go.mod pine-go/go.sum

clean: ## 清理 pine-go / pine-cpp / pine-java build 产物
	cd pine-go && go clean -testcache -cache
	rm -rf pine-cpp/build pine-cpp/build-*
	cd pine-java && mvn -B -q clean
