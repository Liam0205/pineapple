# 从调查到修复的测试策略

本指南描述如何根据缺陷类型选择测试层，以及从调查报告出发高效补测试和修复的策略。

## 适用范围

当任务涉及以下情况时使用本指南：

- 基于调查报告或 `design_doc/` 中的 bug 分析进行修复
- 为已发现的缺陷补充测试覆盖
- 需要判断在哪一层补测试最有效

## 按缺陷类型选择测试层

### 编译器校验类

问题表现：Apple DSL 编译期应拒绝但未拒绝的输入，或校验规则遗漏。

测试策略：

- 优先补 `apple/tests/` 中的 validator / compiler 单测
- 测试目标是确认编译期抛出预期的 `ValidationError`
- 正面和负面用例都要覆盖

### 运行时语义类

问题表现：引擎执行时行为不符合预期（字段投影、trace 内容、skip 语义等）。

测试策略：

- 补 Go engine 集成测试（`pine-go/internal/` 或 `pine-go/integration/` 层）
- 使用真实或测试专用算子构建最小 pipeline 复现问题
- 负面 E2E 验证错误路径下的返回结构（包括 trace/stats 等观测字段）

### Schema / codegen 类

问题表现：算子 Schema 变更导致生成产物与实现不一致。

测试策略：

- 在 Go 注册中修改 Schema
- 运行 `go run ./pine-go/cmd/pineapple-codegen` 重生成
- CI `codegen-check` 自动校验 `git diff --exit-code`
- 若涉及类型映射（`pythonType()`），确认 codegen 映射已覆盖新类型

### 跨层语义类

问题表现：Python DSL、JSON 契约、Go 初始化或运行时之间出现静默语义漂移，最终结果错误但通常不会 crash。

测试策略：

- 这类问题往往对单层测试不可见，必须沿 Python -> JSON -> Go -> result 做端到端追踪
- 优先补至少一个非 happy path E2E，用于覆盖数字 ID、`nil`、未传可选参数等边界值
- 检查 Go 端是否静默丢弃类型断言失败，检查 codegen 是否错误序列化本应省略的可选参数
- 若业务参数值本身引用 metadata 字段名，补编译期一致性校验
- 详细检查方法见 `llmdoc/guides/cross-layer-validation.md`

### 资源 / 上下文链路

问题表现：资源声明、注入或上下文传递链路异常。

测试策略：

- 补专门集成测试，可能需要 test-only 算子读取 `resource.FromContext`
- 当前仓内无内置算子消费资源，E2E 验证需借助测试专用算子

## 有根因分析文档时的策略

当 `design_doc/` 中已有完整根因分析时：

1. **沿最小修复面实施** — 不要在 bugfix 中引入无关重构或范围扩大
2. **先验证 design_doc 假设** — 确认文档中提到的接口或能力在实现中确实存在；design_doc 中的描述可能超前于实现
3. **先修 bug，再更新文档** — 确保修复代码通过测试后，同步更新 design_doc、README 和 llmdoc

## 跟进上游 issue 与临时止血的方法论

当任务是跟进上游依赖（如 wangshu）的 issue、或为其缺陷加下游临时 workaround 时：

### 跨 issue 根因归属：不顺 "follow-up" 措辞合并

issue 之间"follow-up to #N"之类的引用关系是作者主观叙事，**不等于同根因**。判定一个新现象是否属于已提 issue 时，**回读被引用 issue 的正文实际范围**独立判定，而不是顺着 follow-up 措辞并入。

- 反例风险：把标题挂 "follow-up to #100" 的现象并入只讲 pacing 的上游 issue，会让上游误以为是同一修法、只修一半（pacing 与 backing-release 是正交两层，前者修好不蕴含后者）
- 本仓实例：#105 因此**新提独立的 wangshu#11**（arena grow-only / backing release）而非追评只讲 pacing 的 wangshu#9（详见 `llmdoc/memory/reflections/wangshu-rss-growonly-issue105-drop-fat-state.md`）

### 临时止血阈值用 probe 测试实测标定 fixture

止血 workaround 的阈值（如"arena 多大算 fat、该 drop"）**不要拍脑袋取值**，用一个 probe 测试实测标定：

- 写最小 probe 复现"多大输入把被测量推过候选阈值"，取**实测拐点**而非估计值
- 阈值留足余量，让 steady-state **绝不误触发**（本仓实例：取 16× 默认 initial arena，使健康稳态永不命中、只有真 ballooned 的 state 被 drop）
- probe 实测数据本身属任务专属、易过时，**只留在 reflection / probe 测试**，不写进稳定文档当权威常量；稳定文档侧引用代码常量出处即可（如 `pool_wangshu.go` 的 `arenaDropThresholdKB`）

### rc 升级评估：必读上游 issue close comments / release notes

rc / 大版本升级时，"源码看到 API 表面到位"和"issue 真的从根因层关掉"不是同一件事。源码自证只能告诉你"上游做了什么"，**告诉你"上游故意没做什么 / 留作 follow-up 什么"** 的唯一渠道是上游 issue close comments / release notes。

- 反例风险：评估阶段只 grep 源码、看 release notes 跑过 changelog 第一段，看到关键 API 全到位即给出"激进拆除 workaround"的方案。源码可能让你看到 `Arena.Compact()` 存在，但看不到"bump 不回退 / GCRef 不 remap / 留作 follow-up"——这只在 maintainer 留言里
- 本仓实例（`Liam0205/wangshu#11` partial fix）：v0.2.0-rc3 把 `Arena.Compact()` / `ArenaCapKB()` API 表面全做了，但 maintainer 在 close comment 中明确"transient peak 自愈、sustained-fat 仍 latch、full copy-compact 留作 follow-up"。本仓首次踩到这条线，详见 `llmdoc/memory/reflections/wangshu-v020rc3-upgrade-and-workaround-refactor.md`
- 操作约束：rc 升级方案确定前，**必读**触及 issue 的 close comments + 该版本 release notes 全文（不止 changelog 第一段）；这一步与"先读 llmdoc / startup.md"同级，是 rc 升级类任务的入口动作

### workaround 拆除判别：上游修的是 root cause 还是 proxy

拆除 workaround 前**先问**："上游修的是 workaround 防御的 root cause，还是只是 workaround 用的 proxy 观测量？"

- **前者 → 真拆**：上游 API 真等价替换原 workaround 的目标行为
- **后者 → 只换判据**：原 workaround 防御的 root cause 上游只解一半，需保留 workaround 框架、只把判据 / 观测量换成上游新暴露的真观测面

本仓双范例（同一升级窗口内）：
- cadence-sweep **真拆**：原 workaround 目标"让 GC accounting 上的 bytes 真去 sweep"，上游 `MaybeCollectNow()` 真等价替换（host-callable GC trigger），整个 `gcCadenceWangshu` / `collectProg` / `gcReturnCount` 拆除
- drop-fat-state **只换判据**：原 workaround 目标"sustained-fat state 不能让它一直占着 fat backing slab"，上游 `Arena.Compact()` 只解了 transient peak 那一半、sustained-fat 仍 latch；workaround 框架保留，判据从 `GCCountKB`（proxy，sweep 前活跃量）→ `ArenaCapKB`（真观测面，post-Compact cap）

工程信号：**顺序耦合消失是好信号**——若新 API 取代旧 hack 是真的，原 workaround 的脆弱顺序约束（"采样必须早于 sweep"之类）应自然消失；若仍需保留，说明没真取代。本仓 drop-fat-state 判据迁移后旧顺序耦合天然消失，是新设计有效的证据。

## 修复前的验证清单

- 确认目标文件和函数签名与 design_doc 描述一致
- 确认 `__getattr__` 动态分发不会掩盖 API 误用（Apple DSL 中未知方法名会被当作算子名记录）
- 确认变更不破坏 codegen 产物新鲜度（涉及 Schema 时运行 codegen）
- 确认测试覆盖正面和负面路径

## 测试命名与组织

遵循 `conventions.md` 中描述的四层测试结构：

1. `pine-go/internal/` 和 `pine-go/pkg/` 子系统单元测试
2. 算子包单元测试
3. Go 引擎集成测试
4. Python DSL 跨语言测试

优先扩展最近的已有层，而非创建一次性测试风格。
