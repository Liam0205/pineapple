# Admin pprof 端口与信息泄漏面参考

本文档归档 pine-go 可选 admin server 的暴露面、默认状态、运维约束,以及为什么"信息泄漏面默认关闭"是一个有意为之的安全契约。pine-cpp 与 pine-java 不实现 admin pprof,本文为 pine-go 独有特性的边界说明。

## 权威文件

- `pine-go/pkg/server/server.go` — `Config.AdminAddr`、`newAdminMux()`、admin `http.Server` 启停
- `pine-go/pkg/server/server_test.go` — `TestAdminPprofMuxServesPprof` / `TestMainMuxDoesNotExposePprof`(H7) 与三方主端口 pprof 404 断言(M11)
- `pine-go/cmd/pineapple-server/main.go` — `-admin-addr` flag(默认空字符串)
- `scripts/cross-validate/12-extensibility-parity.sh` — Test 7:三 runtime 主端口 `/debug/pprof/` 必须 404
- `scripts/cross-validate/06-error-shapes.sh` — 主端口未知路径返回 404 的对等断言

## 暴露面清单

`newAdminMux()` 仅注册标准 `net/http/pprof` 五条路径,挂在与业务端口完全隔离的独立 `http.Server` 上:

| 路径 | 用途 | 信息泄漏内容 |
|------|------|------|
| `/debug/pprof/` | pprof 首页 + heap/goroutine/allocs/block/mutex/threadcreate 索引 | 进程内存/goroutine 计数、运行时元数据 |
| `/debug/pprof/cmdline` | 进程命令行 | 完整 argv,**含 `-admin-addr`、`-config`、`-listen` 等 flag 取值** |
| `/debug/pprof/profile` | CPU profile 采样(默认 30s,可 `?seconds=N` 拉长) | 函数级 CPU 热点 + 调用图 |
| `/debug/pprof/symbol` | 符号表查询 | 二进制 PC → 函数名映射 |
| `/debug/pprof/trace` | 执行 trace | goroutine 调度事件 + system call 时序 |

主业务 mux(`/health`、`/execute`、`/stats`、`/dag`、catch-all 404)**不**挂载任何 `/debug/pprof/*` handler,这条边界由 `TestMainMuxDoesNotExposePprof` 单元测试与 cross-validate Section 12 Test 7 共同钉住。

## 默认状态:关闭

`Config.AdminAddr` 默认为空字符串(`pine-go/pkg/server/server.go:35`),CLI flag `-admin-addr` 默认为空字符串(`pine-go/cmd/pineapple-server/main.go:23`)。空值时 admin server 不构造、不监听:

```go
if cfg.AdminAddr != "" {
    adminSrv = &http.Server{ ... Handler: newAdminMux(), ... }
    go adminSrv.ListenAndServe()
}
```

也就是说:**默认部署不暴露 pprof**。要启用必须显式 `-admin-addr :6060`(或 `Config{AdminAddr: ":6060"}`),即明确知会运维人员"我要在 6060 上挂一个进程级诊断面"。

## 信息泄漏风险评估

启用 admin 端口后,以下几类信息会经 6060 端口直接暴露:

1. **进程命令行(`/debug/pprof/cmdline`)** — argv 含所有 CLI flag 取值。如果运维通过 flag 传入 `-config /etc/pineapple/secret.yaml` 这类路径,路径片段会在 cmdline 上原文出现;如果业务通过 flag 传入鉴权 token(不推荐),会直接泄漏。
2. **运行时元数据(`/debug/pprof/`)** — heap profile 含分配栈、function name、文件路径,可推断业务逻辑、依赖版本、构建路径。
3. **执行 trace(`/debug/pprof/trace`)** — goroutine 调度细节可推断并发模型与压力曲线。
4. **代码地址布局(`/debug/pprof/symbol`)** — 二进制符号表可辅助 ROP/JOP 利用链构造。

## 运维约束

启用 admin 端口必须满足以下三条之一,否则视为配置事故:

1. **网络层隔离** — admin 端口仅绑回环(`-admin-addr 127.0.0.1:6060`)或内网 ACL/Service Mesh 限制访问源。
2. **认证反代** — 经 nginx/envoy 等反代加 mTLS / Basic Auth 后再放行公网。
3. **诊断窗口** — 仅在故障诊断窗口期临时开启,排障完成立即关闭(SIGTERM 后 admin server 与主 server 一并 graceful shutdown,见 `server.go:297`)。

公网直连 admin 端口的部署被视为 **严重配置漏洞**,等同把 `pprof.Cmdline` 暴露在 internet。

## 独立 server 的设计动机

admin 与主业务跑在两个独立 `http.Server`:

- **超时隔离** — 主端口有 `WriteTimeout: 60s`(默认),`/debug/pprof/profile?seconds=120` 这种长 CPU 采样会被截断。admin server 不设 `WriteTimeout`,放任长 profile 走完(`server.go:273-280`)。
- **绑定隔离** — admin 可单独绑回环,主端口绑公网,不需要复杂的 path-based ACL。
- **mux 隔离** — 主 mux 与 admin mux 是两份 `*http.ServeMux`,生产代码不会因为路径前缀写错而把 pprof 误挂主端口(`TestMainMuxDoesNotExposePprof` 钉住此回归点)。

## 跨运行时对等

pine-cpp 与 pine-java 的 server 都不实现 admin pprof:

- **pine-cpp** — `pine-cpp/src/server/server.cpp` 无 `/debug/pprof` 路由,profiling 走外部工具(perf/gperftools)。
- **pine-java** — `pine-java/.../PineServer.java` 用 `com.sun.net.httpserver.HttpServer`,无 pprof 等价物,profiling 走 JFR/async-profiler。

cross-validate Section 12 Test 7 断言三 runtime 主端口对 `/debug/pprof/` 一律返回 404。任何运行时把 pprof 挂主端口会立即触发 parity 失败。

## 测试断言矩阵

| 断言 | 位置 | 含义 |
|------|------|------|
| admin mux 三条 pprof 路径 200 | `server_test.go::TestAdminPprofMuxServesPprof` | 启用后 admin 必须可达 |
| 主 mux 三条 pprof 路径 404 | `server_test.go::TestMainMuxDoesNotExposePprof` | 主端口必须无 pprof |
| 三 runtime 主端口 `/debug/pprof/` 404 | `12-extensibility-parity.sh` Test 7 | 跨 runtime 主端口零 pprof |
| `Config.AdminAddr` 默认空 | `server.go:35` 字面量 + `main.go:23` flag default | 默认零暴露 |

任何修改其中一条都会触发 CI 失败。
