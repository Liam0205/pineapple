// pine-go benchmarks: a separate submodule.
//
// Rationale: comparison libraries (e.g. gopher-lua / wangshu) are only used for
// performance comparisons and must not leak into the production dependency graph of
// the pine-go main module. Follows the design of the same-named module in wangshu
// (github.com/Liam0205/wangshu).
//
// The main pine-go module is referenced locally through a replace directive, so
// changes to benchmarks never need to be published.
module github.com/Liam0205/pineapple/pine-go/benchmarks

go 1.26.2

require github.com/Liam0205/pineapple/pine-go v0.0.0

require (
	github.com/Liam0205/wangshu v0.2.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/redis/go-redis/v9 v9.18.0 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/yuin/gopher-lua v1.1.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.44.0 // indirect
)

replace github.com/Liam0205/pineapple/pine-go => ..
