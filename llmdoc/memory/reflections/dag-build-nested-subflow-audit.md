---
title: DAG construction + nested SubFlow deep audit
date: 2026-04-30
---

# [DAG construction + nested SubFlow deep audit]

## Task
- 对 DAG 构建与 nested SubFlow 展平/加载链路做一次深度审计，覆盖 Apple 编译器的 `_traverse()` / `pipeline_map` 发射、Go 配置加载的 `expandEntries()`、以及 `internal/dag.Build()` 的边推导逻辑。
- 本次反思聚焦尚未着手修复的高/中/低风险发现，目标是把“哪里存在真实语义缺口、哪里只是当前实现约束、下一步该优先补什么”沉淀为可复用记忆。

## Expected vs Actual
- Expected outcome.
  - 审计应确认 nested SubFlow 在 Apple → JSON → Go flatten 链路下是否保持了正确因果顺序，并验证 `sources`、barrier、skip 与可视化元数据在该链路中的边界语义。
  - 最理想结果是：若存在风险，也应主要是局部错误提示或测试覆盖不足，而不是核心 DAG 因果基线本身存在未校验前提。
- Actual outcome.
  - 审计识别出两个高风险结构问题。
    - `sources` 当前可以引用“未来节点”：Apple 编译器只透传 source 名称，Go 加载期也不校验 source 是否出现在当前算子之前；`dag.Build()` 则无条件加硬边 `src -> current`。一旦用户在声明顺序里把 source 指向后定义算子，就可能引入环或制造违反直觉的因果反转。
    - flatten 顺序本身是 DAG 因果推导的唯一基线：`internal/dag/addEdges` 对扁平 `sequence` 做一次前向扫描来推导 RAW/WAW/WAR、`_row_set_` 与 barrier 相关约束。若 Apple `_traverse()` 或 Go `expandEntries()` 在 nested SubFlow 场景下顺序出错，整张 DAG 都会建立在错误的时间基线上。
  - 同时识别出 5 个中风险与 3 个低风险问题：
    - barrier 是全局全序栅栏，nested SubFlow 的兄弟分支会被过度串行化。
    - `opToSubFlow` 只记录直接父路径，不保留完整 ancestry chain。
    - `skip` 传播目前正确，但依赖 `_traverse()` 中“先 rename、再注入 inherited_skips”的脆弱顺序。
    - 运行时 skip 判断要求 `== true` 的 Go bool；手写 JSON 若写成 `"true"` 或 `1` 会静默失效。
    - `sources` 不能引用整个 SubFlow，nested SubFlow 用户容易误以为可以对一个子树整体建依赖。
    - `::` 前缀拼接当前可避免路径冲突，但依赖 Apple 侧命名约束持续成立。
    - `TopologicalSort` 报错只有 `has cycle`，缺少参与节点信息。
    - nested SubFlow + barrier + sources + control-flow 的组合测试仍不足。

## What Went Wrong
- 审计表明我们此前对 DAG 正确性的信心过多建立在“各层 flatten 逻辑大体看起来一致”这一经验判断上，而没有把“顺序是否被严格验证”视为单独的一类架构风险。
- `sources` 语义长期被当成“用户显式补边”的安全机制来理解，但当前实现实际上默认相信用户引用的是一个因果上已经出现的祖先节点；这个前置条件既没在 Apple 编译期校验，也没在 Go 加载期校验。
- 对 nested SubFlow 的审阅之前更偏向 skip 传播、命名去冲突、结构展开是否正确，而没有把 `_traverse()` / `expandEntries()` 产出的扁平顺序提升为“DAG 全局真相源”来审视，因此没有第一时间意识到它是单点失效面。
- barrier 当前的“从所有过去到自己、再从自己到所有未来”的实现很简单也很稳，但在 nested SubFlow 语义下会把原本结构上互不相关的兄弟子树一起串住；此前默认接受了这一保守策略，却没有明确把它标记为性能/并行度风险。
- `skip`、SubFlow 路径前缀、`opToSubFlow` 这些辅助结构各自局部正确，但很多地方依赖隐式顺序和局部约定，而不是显式不变量或更强的结构表达，因此回归面偏大、错误信息也不够友好。

## Root Cause
- 根因首先是“扁平顺序”在系统里承担了过多职责，但缺少等量的显式约束。它同时服务于 Apple compile-time 校验、Go config flatten、DAG 冒险扫描与部分诊断语义，却没有被文档和测试明确提升为一条核心架构不变量。
- 第二个根因是跨语言边界上的职责断层：Apple 认为 `sources` 已经是最终名字，因此只做透传；Go 认为 source 名存在即可，因此直接建边。两层之间没人承担“source 是否指向过去节点、是否保持因果可解释性”的校验责任。
- 第三个根因是当前实现更偏“最小可运行表示”，而不是“最强可诊断表示”。例如 `opToSubFlow` 只存直接父路径、cycle 报错只给布尔式失败、skip 运行时只接受严格 bool，这些都让实现简单，但把用户理解成本和后续排障成本留在了系统外部。
- 最后，测试矩阵仍然以单机制验证为主，没有系统覆盖 nested SubFlow、barrier、sources、control-flow 的组合边界，因此很多问题只有在深审时才以“架构风险”形式暴露，而不是在日常回归中被自然拦住。

## Missing Docs or Signals
- 适合提升到稳定文档的缺口：
  - `llmdoc/architecture/dag-engine.md` 应明确写出：DAG 的 hazard/barrier/source 推导全部以扁平 declaration order 为基线；Apple `_traverse()` 与 Go `expandEntries()` 的顺序正确性因此属于架构级不变量，而不是普通实现细节。
  - `llmdoc/architecture/apple-compiler.md` 应补充 `sources` 的时序约束，明确 source refs 不仅要是最终算子名，还必须引用声明上早于当前算子的算子；否则应在编译/加载期失败，而不是交给 DAG 构建后用泛化 cycle error 暴露。
  - 相关 DAG/配置文档可补一条关于 barrier 的现状说明：它是全局 full-order fence，语义保守正确，但会牺牲 nested SubFlow 兄弟分支并行度。
  - 稳定文档还可补充 `sources` 不能引用整个 SubFlow，只能引用叶子算子名，避免用户把 `pipeline_map` 路径误当作可依赖对象。
- 更适合先留在 memory 的信号：
  - 做 DAG 深审时，必须先把“因果基线来自哪里”单独拎出来检查；若调度依赖一个 flatten 后的总序，就应优先怀疑 flatten 是否是系统真正的单点故障。
  - 对跨语言字段如 `sources`、`skip`，不要只检查“名称是否传到了另一层”，还要检查“另一层是否补上了该字段隐含的时序/类型语义校验”。
  - 当辅助元数据只用于可视化或诊断时，也应评估它是否弱化了排障能力；例如只存直接父路径、cycle 不报参与节点，平时不影响执行，但会显著抬高调查成本。

## Promotion Candidates
- 适合后续提升到 `architecture/` 或 `reference/`：
  - 在 `llmdoc/architecture/dag-engine.md` 中新增“flatten order 是 DAG 因果基线”的明确不变量说明，并点名 Apple `_traverse()` / Go `expandEntries()` / `internal/dag/addEdges` 三者必须保持一致语义。
  - 在 `llmdoc/architecture/apple-compiler.md` 或相关 reference 中补充 `sources` 的合法性约束：只能引用已声明的叶子算子，且必须位于当前算子之前。
  - 在 DAG 相关稳定文档中记录 barrier 目前是全局全序栅栏，以及这是一种保守正确但可能牺牲并行度的设计。
  - 在测试/guide 文档中加入 nested SubFlow 组合测试建议：至少覆盖 barrier + sources + control-flow + sibling independent branches 的组合。
- 更适合继续保留在 memory：
  - “单次 forward scan 的 sequence 如果是错的，整张 DAG 都会错”这类审计视角属于方法论提醒，先保留为 memory 更灵活。
  - `opToSubFlow` ancestry 信息不足、cycle error 不够可诊断，这些更像后续增强项，可先作为审计 backlog 记忆保留。
  - 手写 JSON 的 skip 值若不是 bool 会静默失效，这属于实现边界警示；若后续确认为真实用户痛点，再决定是否上升为更稳定的 reference 规则。

## Follow-up
- 优先补两项尚未开始的工作：
  - 在 Apple 或 Go 加载期增加 `sources` 时序校验，要求 source 只能引用声明上先于当前算子的算子。
  - 增加 nested SubFlow + barrier + sibling independent branches 的端到端测试，并把 `sources` 与 control-flow 组合纳入同一测试矩阵。
- 中期增强项：
  - 评估在 cycle 报错中返回参与节点集合，降低 DAG 排障成本。
  - 评估是否需要让 `opToSubFlow` 暴露完整 ancestry chain，或至少在诊断/可视化路径中保留更多层级上下文。
