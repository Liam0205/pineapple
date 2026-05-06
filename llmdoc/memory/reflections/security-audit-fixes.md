# [security and correctness audit fixes]

## Task
- 对 Pineapple 执行安全与正确性审计，并修复发现的 8 项问题，覆盖 Lua 沙箱、context 传播、pool 生命周期、HTTP server 加固、错误响应脱敏、注册表严格参数、Python DSL 控制流校验。
- 修复后补充对应单元测试确保不回归。

## Expected vs Actual
- Expected: 修复后系统对恶意/异常输入有更强的防御能力，同时不破坏既有功能。
- Actual: 全部 8 项修复落地，新增 11 个测试用例覆盖新行为。既有测试全部通过。testdata 中的幻象参数（`field`、`dedup_by`、`common_field`、`item_field`）被清理以满足严格参数校验。

## Key Decisions

### Lua 沙箱采用白名单模型
- 选择 `SkipOpenLibs: true` + 显式加载安全库，而非 blacklist 移除危险库。
- 原因：白名单模型在 GopherLua 新增内置库时不会意外暴露新能力；显式禁用 `dofile`/`loadfile` 是双重保险。

### PanicError 拆分 Error() vs DetailedError()
- 不修改 HTTP handler 的错误返回逻辑，而是在 error 类型本身做信息分级。
- 原因：所有通过 `.Error()` 暴露给调用方的路径自动获得脱敏保护，无需逐处修改 handler；需要 stack trace 的日志路径显式调用 `DetailedError()`。

### Registry 严格拒绝未声明参数
- 选择拒绝而非静默忽略未声明参数。
- 原因：静默忽略是 typo 的温床——用户写错参数名后行为变成 silent no-op，debug 成本极高。Fail-fast 在引擎加载期暴露问题。
- 代价：已有 testdata 中的幻象参数必须清理。

### Server 超时采用可配置默认值
- 不使用 `net/http` 默认的零值（无超时），而是提供有限但合理的默认值。
- `ReadHeaderTimeout` 5s 防 slowloris；`WriteTimeout` 30s 兼容多数算子执行时长；均可通过 Config 或 CLI flag 覆盖。

### 控制流校验前移到声明期
- `elseif_()` after `else_()` 和 duplicate `else_()` 的校验放在 `apple/flow.py` 而非 `apple/compiler.py`。
- 原因：声明期校验让错误 stack trace 直接指向用户代码行，而编译期报错已经丢失了原始调用栈。

## What Went Wrong
- testdata 中存在多个从未被 Go 运行时消费的幻象参数（`field`、`dedup_by` 等），说明此前缺少对 JSON fixture 与 Schema 一致性的系统校验。
- 严格参数校验是 breaking change：开启后所有消费 JSON 配置的路径都必须确保参数名精确匹配 Schema。

## Missing Docs or Signals
- 稳定文档此前对 HTTP server 安全加固（超时、body 限制）无任何提及。
- `PanicError` 的信息分级策略属于 API 面安全契约，应在架构文档中有记录。
- Lua 沙箱的具体能力边界（哪些库可用、哪些被禁）应记录在算子契约参考中。

## Follow-up
- 考虑为 `MaxRequestBodySize` 添加 per-endpoint 配置能力（当前是全局统一限制）。
- 考虑在 CI 中加入 testdata JSON 与 Schema Params 的一致性检查，避免幻象参数再次积累。
- Lua 沙箱可进一步评估是否需要 instruction count 限制，防止 CPU 密集型无限循环。
