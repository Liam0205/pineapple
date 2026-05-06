# [Medium-Severity Audit Fixes -- Round 2]

## Task
- 修复安全与正确性审计第二轮发现的 6 项中等严重度问题，涵盖 goroutine 泄漏、全局状态竞态、SSRF、无界响应读取、错误吞没。
- 具体修复：watchConfig goroutine 泄漏 (context+select)、Server 全局状态重构为结构体、log.SetPrefix 竞态 (sync.Once)、remote_pineapple SSRF 防护、io.ReadAll 替换为 LimitReader、Redis 基础设施错误透传。

## Expected vs Actual
- Expected: 6 项修复落地，向后兼容，既有公共 API 保持不变，测试全部通过。
- Actual: 全部修复在单次 commit 中完成并通过测试。Server struct 重构需要同步更新所有测试 helper 从包级状态切换到实例模式，但公开 `Run(cfg Config) error` API 未变。

## What Went Wrong
- Server 全局状态重构的波及面比预期大：所有测试之前通过包级原子指针注入引擎，重构后需改为在 Server 实例上操作。这是前一轮测试覆盖率补齐 (test-coverage-server-transform.md) 中建立的模式被一次性替换。
- `allow_private` 和 `fail_on_error` 是新增的算子行为开关，当前仅在代码中存在，算子契约文档未同步更新。

## Root Cause
- 第一轮审计 (security-audit-fixes.md) 已明确记录"HTTP server 安全加固无任何提及"，但该缺口一直没有促进文档更新，直到本轮重构彻底改变了 server 内部结构。
- 算子级 feature flag 的文档义务不明确：`operator-contract.md` 定义了 Schema 注册与参数声明规范，但没有要求行为模式开关（如 fail_on_error、allow_private）在文档中显式列出安全含义。

## Missing Docs or Signals
- `dag-engine.md` 的 server 小节仍描述旧的包级状态模型，需更新为 Server struct 生命周期（创建、watchConfig with context、graceful shutdown propagation）。
- `operator-contract.md` 缺少对网络调用类算子的安全约束说明（init-time DNS 校验 + dial-time IP guard 的 SSRF 防护模型）。
- `conventions.md` 未提及 `io.LimitReader` 作为读取外部响应的安全默认模式，也没有约定 `max_response_size` 类参数的默认值策略。
- Redis 错误透传模型 (`SetWarning` for non-fatal infra errors + `fail_on_error` for strict mode) 是跨算子可复用的错误处理模式，当前仅存在于实现中。

## Promotion Candidates
- **适合提升到 `llmdoc/architecture/dag-engine.md`**:
  - Server 从全局状态改为 struct-based、watchConfig 生命周期管理（context 传入 + ticker/select + 退出时 cancel）。
- **适合提升到 `llmdoc/reference/operator-contract.md`**:
  - 网络调用类算子的 SSRF 防护契约：init-time 域名解析校验 + 自定义 Transport 的 dial-time IP 检查。
  - `fail_on_error` 模式约定：基础设施错误默认 warning 降级、通过 `fail_on_error=true` 切换为严格模式。
- **适合提升到 `llmdoc/must/conventions.md`**:
  - 读取外部响应时必须使用 `io.LimitReader`，禁止裸 `io.ReadAll`；`max_response_size` 默认 10MB。
  - `sync.Once` 保护全局可变状态（如 log prefix）是热加载场景的标准模式。
- **暂留 memory**:
  - Server struct 重构的具体 test helper 迁移模式（从包级原子指针到实例方法）。
  - `ssrf.go` 中 `isPrivateIP` 的具体 CIDR 列表与判定逻辑。

## Follow-up
- 更新 `dag-engine.md` 中的 server 架构描述，反映 struct-based 生命周期与 context 传播。
- 在 `operator-contract.md` 增加"网络安全约束"小节，记录 SSRF 防护模型与 LimitReader 要求。
- 在 `conventions.md` 增加"外部 I/O 安全默认值"条目，固化 LimitReader + sync.Once 模式。
- 考虑为 `allow_private` 参数增加环境级 override（如测试环境默认 allow），避免开发调试时频繁设置。
