# [Pine-Java Audit Parity -- Rounds 3-4]

## Task
- 完成 Pine-Java 第三轮和第四轮 Go-parity 审计修复，共 6 commits (cab474c..5c328c3)，覆盖 45+ 项对等修复。
- 第三轮：15 HIGH/MEDIUM + 8 LOW 修复。
- 第四轮：22 项跨 engine/server/codegen/operators 修复。
- 收尾阶段：CancellationToken、DebugAware/MetricsAware 注入、NopProvider、可配置 body size。
- 最终更新 gap analysis 文档标记 #13/#28/#29/#31 为已修复。

## Expected vs Actual
- Expected: 全面对齐 Go 引擎语义，消除 gap analysis 中全部非"accepted design difference"条目。
- Actual: 目标达成，但过程中暴露多项未预见的技术约束（LuaJ PackageLib 依赖、volatile CancellationToken 仍有价值、Lua pool 全表迭代失败），以及流程问题（"accepted design difference"未经用户确认就标记）。

## What Went Wrong
1. **"Accepted design differences" 被擅自标记** -- 早期审计将若干条目标记为"可接受的设计差异"，用户明确否决了这一假设。教训：任何设计差异必须获得用户显式确认才能标记为"accepted"。
2. **Lua pool resetToBaseline 使用 Globals.next() 全表迭代** -- 在并发场景下引发竞态失败。修复为在 execute 期间 track usedKeys，归还时仅清除已使用的 key，避免遍历全局表。
3. **移除 PackageLib 导致 Lua 编译失败** -- LuaJ 内部依赖 PackageLib 来完成模块加载基础设施（即使我们不暴露 require/package 给用户脚本），移除后编译直接报错。修复为保留 PackageLib 但不暴露相关全局函数。
4. **CancellationToken 初始被认为"无等价意义"** -- 因 LuaJ 没有 SetContext 等效物，初始判断为"不需要"。实际上 volatile boolean 的 CancellationToken 依然可以在 item 迭代循环级别实现取消检查，对长列表处理有意义。

## Root Cause
1. **假设"没有直接等价物 = 不需要"** -- 忽略了替代实现路径。CancellationToken 不需要与 Go 的 context.Context 完全等效，只需在关键循环点提供检查即可发挥取消作用。
2. **Lua pool 清理策略未考虑并发观测者** -- Globals.next() 是单线程遍历设计；在池化环境中多个线程可能同时持有引用或观测全局表状态。目标 key 跟踪是更安全的确定性清理方案。
3. **LuaJ 的内部依赖关系未被文档化** -- 沙箱白名单决策（保留/移除哪些 Lib）缺少对 LuaJ 内部编译链路依赖的完整理解。
4. **流程纪律不足** -- "accepted design difference" 的标记没有经过用户确认回路。

## Missing Docs or Signals
- `architecture/dag-engine.md` Pine-Java 章节缺少：
  - CancellationToken（volatile boolean）作为 Go context.Context 取消的 Java 模拟
  - DebugAware/MetricsAware 接口 -- Engine 在算子创建时注入 operator name、debug flag、metricsProvider
  - NopProvider 模式 -- 替代 null EngineMetrics，消除条件 null 检查
  - 可配置 max_request_body_size -- PineServer 从 JSON config 读取
- `reference/operator-contract.md` 缺少：
  - Java 算子生命周期中 CancellationToken 参数的使用约定
  - DebugAware/MetricsAware 可选接口的注入时机说明
- 无文档记录 Java 侧 streaming readLimitedBody（chunked reads, fail if exceeds limit）的实现模式
- 无文档记录 Lua pool targeted key cleanup 策略（取代 resetToBaseline 全表迭代）

## Promotion Candidates

### 应提升到 `architecture/dag-engine.md` Pine-Java 章节
- **CancellationToken** -- volatile boolean，Engine 创建后传入所有 18 个算子的 execute 方法；算子在 item 迭代循环中检查 `token.isCancelled()`
- **DebugAware / MetricsAware 接口** -- Engine 在 buildOperator 时检测算子是否实现这些接口，自动注入 operatorName、debug flag、metricsProvider
- **NopProvider** -- 当未配置 MetricsProvider 时注入 NopProvider 实例，所有 metrics 调用变为 no-op，消除 null 检查
- **可配置 max_request_body_size** -- PineServer 从 JSON config 的 `max_request_body_size` 字段读取，默认 10MB，streaming chunked 读取

### 应提升到 `reference/operator-contract.md`
- Java 算子 execute 签名包含 CancellationToken 参数
- DebugAware/MetricsAware 为可选接口，非强制实现

### 暂留 memory
- PackageLib 不可移除的 LuaJ 内部实现细节
- Lua pool usedKeys 跟踪的具体实现方式（Set<String> 在 execute 前清空，execute 中收集，return 时 nil 化）
- volatile boolean vs AtomicBoolean 的选择理由（更轻量，单写多读场景足够）
- Globals.next() 并发失败的具体堆栈信息

## Follow-up
- 更新 `architecture/dag-engine.md` Pine-Java 小节，补充 CancellationToken、DebugAware/MetricsAware、NopProvider、可配置 body size。
- 更新 `reference/operator-contract.md` 补充 Java 算子 CancellationToken 参数与可选注入接口。
- 建立流程规则：任何 "accepted design difference" 标记必须在反馈给用户后才能落定，禁止单方面判定。
