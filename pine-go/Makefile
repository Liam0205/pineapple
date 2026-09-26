# pine-go Makefile — sits alongside the top-level Makefile and offers finer-grained,
# Go-only entry points.
#
# Modelled on the wangshu (github.com/Liam0205/wangshu) Makefile so the same muscle
# memory applies: `make test` / `make bench` behave the same wherever you run them.
.PHONY: help all fmt lint test bench race cover fuzz tidy

help: ## List all targets
	@awk 'BEGIN {FS=":.*##"; printf "pine-go targets (run \033[36mmake <target>\033[0m):\n\n"} \
	     /^[a-zA-Z_-]+:.*?##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

all: fmt lint test ## Full local check for pine-go

fmt: ## gofmt -w every .go file (pine-go subtree only)
	@files=$$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './vendor/*'); \
	  [ -z "$$files" ] || gofmt -w $$files

lint: ## golangci-lint (uses the repo-root .golangci.yml if present)
	golangci-lint run ./...

test: ## Unit tests (with race)
	go test -race ./...

bench: ## go test -bench (defaults to the benchmarks/ submodule + pine_bench tag; for the main module use `make bench BENCH=./internal/...`; for a gopher-lua comparison use `make bench TAGS=lua_gopher`, TAGS is added on top of pine_bench)
ifdef BENCH
	go test -tags='pine_bench $(TAGS)' -bench=. -benchmem -count=1 -run='^$$' $(BENCH)
else
	cd benchmarks && go test -tags='pine_bench $(TAGS)' -bench=. -benchmem -count=1 -run='^$$' ./...
endif

race: ## Race only (test already runs with race; kept for parity with wangshu)
	go test -race ./...

cover: ## Coverage report + terminal summary
	go test -race -coverprofile=coverage.out -covermode=atomic ./...
	go tool cover -func=coverage.out | tail -1

fuzz: ## Discover every func Fuzz* and smoke-run each for 30s (reuses the repo-root scripts/go-fuzz.sh)
	bash ../scripts/go-fuzz.sh 30s

tidy: ## go mod tidy (main module + benchmarks submodule) + git diff check
	go mod tidy
	cd benchmarks && go mod tidy
	git diff --exit-code go.mod go.sum benchmarks/go.mod benchmarks/go.sum
