# pine-go 单语言 Makefile —— 与顶层 Makefile 平行,提供 Go 视角的精细入口。
#
# 设计参考 wangshu(github.com/Liam0205/wangshu)的 Makefile, 同一 muscle memory:
# 在仓内任一位置 `make test` / `make bench` 行为一致。
.PHONY: help all fmt lint test bench race cover fuzz tidy

help: ## 列出所有 target
	@awk 'BEGIN {FS=":.*##"; printf "pine-go targets (run \033[36mmake <target>\033[0m):\n\n"} \
	     /^[a-zA-Z_-]+:.*?##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: fmt lint test ## pine-go 本地全检

fmt: ## gofmt -w 所有 .go(仅 pine-go 子树)
	@files=$$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './vendor/*'); \
	  [ -z "$$files" ] || gofmt -w $$files

lint: ## golangci-lint(沿用仓根 .golangci.yml 若存在)
	golangci-lint run ./...

test: ## 单元测试(含 race)
	go test -race ./...

bench: ## go test -bench(默认走 benchmarks/ 子 module; 主 module 用 `make bench BENCH=./internal/...`; gopher-lua 对照用 `make bench TAGS=lua_gopher`)
ifdef BENCH
	go test $(if $(TAGS),-tags='$(TAGS)') -bench=. -benchmem -count=1 -run='^$$' $(BENCH)
else
	cd benchmarks && go test $(if $(TAGS),-tags='$(TAGS)') -bench=. -benchmem -count=1 -run='^$$' ./...
endif

race: ## 仅 race(test 已含 race,本 target 兼容 wangshu 习惯)
	go test -race ./...

cover: ## 覆盖率产物 + 终端摘要
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -1

fuzz: ## 自动发现 func Fuzz* 各跑 30s 冒烟(沿用仓根 scripts/go-fuzz.sh 实现)
	bash ../scripts/go-fuzz.sh 30s

tidy: ## go mod tidy + git diff 守门
	go mod tidy
	git diff --exit-code go.mod go.sum
