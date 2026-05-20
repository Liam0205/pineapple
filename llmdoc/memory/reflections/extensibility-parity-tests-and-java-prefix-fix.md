---
name: extensibility-parity-tests-and-java-prefix-fix
description: Java prefix-match 第二次修复、cross-validate 第 12 层扩展点对等测试、三引擎单元测试补齐、版本 0.7.1 发布
type: reflection
---

## Task

在 audit-extensibility-blindspot 复盘后，落地具体修复与测试：

1. 修复 Java HttpServer prefix-match 语义泄露（`8cd448a`）：`/health/sub/path` 因 longest-prefix 匹配命中 `/health` context 返回 200，需要 exact-path guard。
2. 新增 cross-validate 第 12 层 `12-extensibility-parity.sh`（`0ae4756`）：6 项检查覆盖 404 状态/body/content-type 对等、POST 未知路径、多任意路径、深层嵌套路径。
3. 三引擎单元测试：Go `TestHandleNotFound_ReturnsJSON404` + `TestMiddlewareCanInterceptCustomPath`；Python 7 项测试覆盖 404 JSON、middleware 拦截、post-start 拒绝添加。
4. 版本升至 0.7.1，全测试绿色。

## Expected vs Actual

- **预期**：上一轮 root fallback context 修复后，Java 未注册路径行为已与 Go/Python 对齐。
- **实际**：root fallback context 仅让 middleware 可见所有请求，但 HttpServer 的 longest-prefix matching 使得 `/health/sub/path` 仍匹配到 `/health` handler 而非 fallback，返回 200 而不是 404。cross-validate test 6（deep nested path）捕获了这个 bug。

## What Went Wrong

1. **第一次修复 (root fallback) 不充分**：仅解决了"middleware 不可见未注册路径"的问题，未意识到 prefix matching 是同一能力等价类别下的第二个独立语义差异。
2. **Java HttpServer 的 prefix-match 行为是隐式的**：`createContext("/health", handler)` 会匹配所有以 `/health` 为前缀的路径，这与 Go `http.HandleFunc("/health", ...)` 的精确匹配行为不同。这个平台差异在之前 19 轮审计中从未被触及。

## Root Cause

Java `com.sun.net.httpserver.HttpServer` 的路由模型与 Go `net/http` 根本不同：
- Go：`HandleFunc("/health", h)` 仅精确匹配 `/health`（除非路径以 `/` 结尾表示子树）。
- Java：`createContext("/health", h)` 匹配所有前缀为 `/health` 的路径（longest-prefix-wins）。

第一次修复只在"根路径 fallback"层面补齐了 middleware 可见性，没有在每个 handler 内部做 exact-path 验证。两个问题属于同一"路由语义差异"范畴，但表现为不同症状。

## Missing Docs or Signals

1. **Java HttpServer 路由模型的平台差异**从未被记录在 llmdoc 中。`dag-engine.md` 的 Pine-Java 章节缺少 HTTP server 路由层实现细节。
2. **第一次修复的 commit message 或 reflection 未标注"prefix matching 仍待处理"**——导致 incomplete fix 被认为是 complete fix。
3. **cross-validate 的 TOTAL_SECTIONS 硬编码问题再次出现**（第三次）：从 11 扩展到 12 时需要同时更新 `cross-validate.sh` 和 `_env.sh`，这个维护负担已在 p2-refactor 和 pine-python-and-v07 两次复盘中被指出，但仍未自动化。

## Promotion Candidates

### 应提升到 `must/conventions.md`
- Java HttpServer handler 必须包含 exact-path guard（`!exchange.getRequestURI().getPath().equals(registeredPath)` 则委托 fallback），因为 createContext 是 prefix-match 语义。这是跨引擎 HTTP 路由对等的硬性要求。

### 应提升到 `guides/cross-layer-validation.md`
- 第 12 层"扩展点对等"验证的存在及其检查项（404 parity、middleware visibility、deep nested path）。
- "深层嵌套路径"作为 prefix-match 回归检测的标准探针。

### 应提升到 `architecture/dag-engine.md`
- Pine-Java HTTP server 路由层使用 prefix-match（HttpServer.createContext），需要 exact-path guard 来实现与 Go 精确匹配语义的对等。

### 仅保留在 memory
- 具体的 `wrapHandler` 实现细节。
- test 6 的具体 URL 路径选择（`/health/sub/path`）。
- 版本 0.7.1 的发布时间线。

## Follow-up

1. 考虑将 TOTAL_SECTIONS 从硬编码改为自动计数（`ls scripts/sections/*.sh | wc -l`），根除第三次重复的教训。
2. 更新 `dag-engine.md` Pine-Java 章节补充 prefix-match guard 说明。
3. 更新 `guides/cross-layer-validation.md` 层数为 12 并描述扩展点对等层。
4. 更新 `must/conventions.md` 中跨验证层数描述。
