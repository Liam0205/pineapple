# Pine-Java 第十六轮 Go-parity 审计复盘

## Task
- 第十六轮审计修复（commit c119a51），处理 2 项差异：TransformByLua pool.borrow() null 返回模式、Engine.renderDAG ValidationError 类型迁移。

## Expected vs Actual
- Expected: 第十五轮声明 0 HIGH/2 MEDIUM deferred，本轮应为收尾性质的残余清扫。
- Actual: 确实仅 2 项，均为错误类型迁移（IllegalStateException/IllegalArgumentException -> 结构化 PineErrors）。与预期吻合，审计进入最终收敛阶段。

## What Went Wrong

### M1: TransformByLua pool.borrow() throw -> return null + caller check
Go 侧 pool.Borrow() 返回 (value, error)，调用者自然获得错误并决定如何包装。Java 原始实现在 pool.borrow() 内部直接 throw IllegalStateException（pool closed 时），绕过了调用者对错误分类的控制权。修复为 borrow() 返回 null，execute() 检查 null 后 throw OperatorException。

### M2: Engine.renderDAG IllegalArgumentException -> ValidationError
unsupported format 使用 IllegalArgumentException 而非结构化 ValidationError。属于常规错误类型迁移。

## Root Cause

1. **Go 多返回值 vs Java 异常位置差异** -- Go 的 (value, error) 模式天然将错误决策权交给调用者。Java 中将 throw 放在被调用方内部是惯用写法，但违背了"调用者决定错误类型"的 parity 原则。这不是知识缺失，而是 Java 惯用法与 Go 错误模型的系统性摩擦。每次发现这类差异都需要判断：是让被调用方 throw 正确类型（简单但耦合），还是让调用者 check-and-throw（解耦但多一步）。本轮选择后者以匹配 Go 语义。

2. **OperatorException/错误类型迁移长尾** -- 这是自第九轮引入 OperatorException 以来的第六轮连续清扫（第 9/10/12/13/14/15/16 轮均涉及）。IllegalArgumentException 在第十四轮已被识别为需要迁移的类型之一，但 renderDAG 的实例直到本轮才被发现。根因是 renderDAG 不在算子执行路径上（是 DAG 可视化工具方法），前几轮审计聚焦算子路径时自然遗漏。

## Missing Docs or Signals

- 无文档明确声明"所有 public API 方法的错误类型必须使用结构化 PineErrors，包括工具方法（renderDAG）和资源管理方法（pool.borrow）"。当前文档聚焦于算子执行路径的错误模型，非执行路径方法的覆盖是隐含的。
- 无文档描述 Go (value, error) -> Java 映射的决策模型：何时在被调用方直接 throw 结构化异常，何时返回 null/Optional 让调用者 throw。

## Promotion Candidates

### 暂留 memory（不需提升到稳定文档）
- **Go (value, error) -> Java 映射模式** -- pool.borrow() 的 null 返回模式是特定于 pool closed 场景的局部决策，不足以抽象为通用规则。保留在 memory 中作为参考案例。
- **非执行路径方法的错误类型覆盖** -- renderDAG 是唯一的非算子工具方法，不值得为此扩展稳定文档。

### 不需要稳定文档更新
- 两项修复均为错误类型迁移的延续，不引入新架构概念或 API 行为变更。

## Follow-up
- 审计已连续三轮（14/15/16）保持 2 项/轮。结合第十五轮的 "0 HIGH" 结论，Pine-Java parity 审计可正式结束。
- 如需最终确认，可执行宽模式 grep 验证无残余非结构化异常：`grep -rn "throw new \(IllegalStateException\|IllegalArgumentException\|UnsupportedOperationException\)" pine-java/src/main/`
- OperatorException 迁移历史总结：第 9 轮引入 -> 第 10/12/13/14/15/16 轮清扫共 7 轮，涉及 IllegalStateException、IllegalArgumentException、UnsupportedOperationException 三种原始类型。长尾根因是迁移范围逐步扩展（先算子内 -> 再 pool/工具方法）。
