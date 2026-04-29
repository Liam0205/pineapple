# [nested SubFlow multi-skip 设计缺口复盘]

## Task
- 将 Apple DSL 中控制流算子的 `skip` 从单个字符串扩展为字符串列表，以支持 `if_().add_subflow(sf).end_if_()` 这类嵌套 SubFlow 场景。
- 编译器同时增加了外层分支控制字段向嵌套 SubFlow 内部算子传播的逻辑。
- 本次复盘聚焦 commit `dadd82f` 的原始设计为何遗漏了若干边界条件，最终由协作者在 commit `d25cc4b` 中补修。

## Expected vs Actual
- Expected outcome.
  - `skip: str -> list[str]` 之后，所有受控制流影响的算子都应完整携带其所在上下文的全部控制字段，无论它们是直接挂在嵌套 `if_()` 中，还是位于 SubFlow 内/外。
  - 编译过程应保持幂等；同一个 `Flow` 多次 `compile_dict()` 应输出完全一致的结果。
  - SubFlow 内部控制字段在 compile 时被重命名加前缀后，所有继承链路上的引用也应同步更新。
- Actual outcome.
  - 原始实现只覆盖了“外层 branch -> 内层 SubFlow”这条主路径，漏掉了三类关键边界：直接嵌套 `if_()` 的多重 guard 附着、编译阶段对 IR 的原地修改导致的非幂等性、以及 DSL 声明期记录的 `_parent_skips` 与编译期重命名后的字段名漂移。
  - 这些缺口最终在 Harry-Chen 的后续修复中分别通过遍历完整 control stack、在 traversal 起点 `deepcopy`、以及增加字段重命名映射修补逻辑得到解决。

## What Went Wrong
- 我把这次改动理解成一次“字段类型扩展 + SubFlow 继承补齐”，于是实现和验证都围绕目标 happy path 展开，没有重新盘点 `skip` 的全部生产者与消费者。
- 直接挂在嵌套 `if_()` 里的业务算子仍走 `_add_op()` / `BaseOp._apply()` 这条老路径，而这条路径原本只读取 `self._ctrl_stack[-1].branches[-1]`。单字符串时代这成立，因为一次只需要一个字段；改成列表后，算子实际上同时处于多个控制分支内，应聚合整个 control stack，但我没有回头重审这一前置假设。
- 编译器 traversal 被我当作“读取 Flow 并发射 JSON”的过程来思考，忽略了它其实会原地改写 IR：既会重命名控制字段，也会向 `skip` 列表 `append()` 继承字段。这样一来，第二次编译不再从原始声明态开始，自然会积累脏状态。
- `_parent_skips` 在 `add_subflow()` 时捕获的是 DSL 声明期名字，而控制字段前缀重命名发生在 compile traversal 时。我当时只考虑“把父级 skip 带下去”，没有考虑“带下去的是一个稍后还会被改名的符号”，导致 child SubFlow 继承了过期字段名。
- 测试集缺少三个负向断言：直接嵌套 `if_` 是否附着所有外层 guard、连续两次编译是否一致、SubFlow 嵌在父 SubFlow 内部控制分支下时 skip 名称是否跟随重命名链路更新。这让设计盲点在实现阶段没有被及时暴露。

## Root Cause
- 根因首先是单路径思维。需求由“SubFlow 在 branch 中丢失 skip”触发，我的分析也就过度绑定在这一个场景上，没有把 `skip` 视为控制流降级机制中的横切字段，去系统性枚举所有既有控制流组合。
- 根因其次是对 IR 变异性的认识不足。原有实现因为 `skip` 是单值赋值，很多原地修改问题被掩盖了；改成列表后，`append()` 把潜伏的非幂等性立刻放大，但我没有把“traversal + mutation”当作编译器设计中的高风险信号来处理。
- 更深一层的根因是没有识别 DSL 声明期名称与 compile 期最终名称之间的两阶段差异。SubFlow 前缀重命名本质上引入了延迟绑定的名字系统，而原始实现仍按“声明时拿到的字段名就是最终字段名”来传递引用，导致跨阶段引用失真。
- 最后，验证策略仍偏 happy path：证明“新场景能工作”了，但没有证明“旧场景不被破坏”、“多次编译不漂移”、“跨层重命名链路保持一致”。

## Missing Docs or Signals
- 现有稳定文档对 Apple 控制流降级有总体描述，但没有把 `skip` 明确标成一个会被多个代码路径共同读写的横切编译字段，因此在做类型变更时，缺少一个显式信号提醒开发者检查全部 attach / inherit / rename / emit 路径。
- 也缺少关于“编译 traversal 是否允许原地修改 Flow/SubFlow IR”的稳定约定。没有这个约定时，开发者容易把 traversal 当成只读过程，而忽略幂等性风险。
- 对字段重命名的阶段边界说明不足：文档没有提醒控制字段在 DSL 声明期和 compile 产物中可能不是同一个名字，因此凡是提前缓存字段引用的逻辑，都应经过 rename 映射修正。
- memory 层面，这次最关键的经验是方法论检查项，而不是新架构事实：当改动一个跨多路径共享的数据结构时，要列全所有 producer/consumer；当 traversal 会改 IR 时，要优先 clone 后 mutate，并增加 compile idempotency 测试。

## Promotion Candidates
- 适合后续提升到稳定文档：
  - 在 `llmdoc/architecture/apple-compiler.md` 中补充控制流降级的实现注意事项，明确 `skip` 属于横切控制字段，任何类型或语义变更都必须检查直接 attach、SubFlow 继承、字段重命名与最终发射四类路径。
  - 在同一文档或相关 guide 中补充编译器幂等性原则：如果 traversal 需要修改 IR，应优先 clone-then-mutate，并把 `compile(flow) == compile(flow)` 视为默认测试项。
  - 增加一条关于“声明期字段名 vs 编译期重命名字段名”的说明，提示所有缓存字段引用的逻辑都需要经过 rename pipeline 验证。
- 更适合先留在 memory：
  - 本次遗漏暴露出的思维模式问题，即“只修触发问题的那条路径，而没有盘点同一字段的全部既有使用点”，可作为后续所有跨切面编译器改动的通用提醒。
  - 对负向测试集合的补齐经验，可先作为 memory 中的检查清单保留，等类似问题重复出现时再升格成更正式的测试 guide。

## Follow-up
- 后续若再做 Apple 编译器中的横切字段变更，应先画出该字段的 producer/consumer 清单，再补三类默认测试：多层控制流附着、跨阶段重命名链路、compile 幂等性。
- 若未来再次出现类似“声明期名称与编译期名称漂移”的问题，应考虑把字段 rename 映射与引用修正模式正式写入 `llmdoc/architecture/apple-compiler.md` 或相关 guide，避免继续依赖个人记忆。
