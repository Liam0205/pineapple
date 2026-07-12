// pine-go benchmarks 独立子 module。
//
// 设计动机:对照基准库(如 gopher-lua / 未来 wangshu)只用于性能对比,
// 不应污染 pine-go 主 module 的生产依赖图。参考 wangshu(github.com/Liam0205/wangshu)
// 同名设计。
//
// 主 module pine-go 通过 replace 指令本地引用,benchmarks 改动无需 publish。
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
