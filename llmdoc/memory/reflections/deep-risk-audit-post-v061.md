# [deep risk audit after post-v0.6.1 fixes]

## Task
- 在完成风险 1-3、5-6 的修复后，对 Pineapple 进行一次面向运行时正确性与运维稳健性的深度风险审计。
- 审计覆盖 `pkg/server/server.go`、`internal/runtime/parallel.go`、`internal/runtime/scheduler.go`、`operators/lua/pool.go`、`operators/lua/lua.go`、`internal/dataframe/column_frame.go`、`apple/validator.py`、`pine.go`，并记录新发现风险与仍存在的既有低优先级问题。
- 本次复盘同时记录调查方法本身的有效性，尤其是 `llmdoc:investigator` 在广域审计任务中的表现。

## Expected vs Actual
- Expected outcome.
  - 在前一轮缺陷修复后，系统应主要剩余零散边界问题；审计应帮助确认修复是否已消除高风险区，并补充少量后续优化项。
  - 对热加载、Lua 生命周期、数据并行、列存输入构造、跨语言配置一致性等区域，应能形成清晰的风险分级与后续修复建议。
- Actual outcome.
  - 审计新识别出 6 个风险，并确认 2 个既有低优先级问题仍然存在。
  - 其中 3 个高优先级问题集中在生命周期管理与隔离语义，而不是 DAG/算法主体：
    - `pkg/server/server.go` 中 engine 与 resources 分两次原子替换，热加载窗口内可能出现“新 engine + 旧 resources”的不一致快照。
    - `operators/lua/pool.go` 缺少 pool 级 `Close()` 生命周期，热加载替换 engine 后旧 Lua VM/cgo 资源无法释放。
    - `operators/lua/pool.go` 归还状态时只删除新增 global key，不恢复对基线 global 的覆盖，导致跨请求 Lua 环境污染。
  - 2 个中优先级问题说明并发与数据语义上仍有隐含契约未显式建模：
    - `internal/runtime/parallel.go` 的 `data_parallel` 默认要求同一 `cop.Instance` 在单请求内可重入并发执行，但没有 capability 标记或运行时保护。
    - `internal/dataframe/column_frame.go` 的 `BuildInput` 未读取 `present` bitmap，显式 `nil` 与字段缺失被混淆，可能造成 row/column 模式语义分叉。
  - 1 个低优先级问题是 `apple/validator.py` 与 `pine.go` 各自维护 `data_parallel unsafe transforms` 列表，存在跨语言手工同步漂移风险。
  - 此外，两个既有低优先级问题仍在：`pine.go` 通过 `slog.SetDefault()` 修改进程级 logger；scheduler 仅保留第一条 fatal error，其他并发 fatal 会被静默丢弃。

## What Went Wrong
- 即使前一轮已修复多项高风险问题，最容易残留的仍不是显式业务逻辑错误，而是 reload / teardown / isolation 这类生命周期边界；如果只围绕功能正确性回归，很容易误判系统已经足够稳健。
- 先前对 server hot-reload 的设计关注点偏向“分别原子替换 engine 与 resource manager”，但没有继续追问“请求侧是否观察到一致快照”。单个字段原子安全不等于跨字段组合语义安全。
- Lua pool 设计更关注 borrow/return 的复用效率，而没有把 engine teardown 视为第一等生命周期事件，导致 cgo 资源释放路径缺位。
- 对 Lua state 清理的思路偏“删除本次新增内容”，默认基线环境本身不会被污染；这个假设过强，忽略了请求代码可以覆盖已有 stdlib/global 的事实。
- `data_parallel` 首版设计把“对算子实现透明”作为主要目标，但未把“算子是否可重入并发调用”上升为显式契约，因此运行时默认行为实际上比文档暴露的能力更强。
- `ColumnFrame.BuildInput` 的实现沿用了列式读取路径，却遗漏了 `present` bitmap 这一缺失值语义载体，说明列存优化路径与原始输入语义之间还缺少系统性对照检查。
- Python/Go 双端维护 unsafe transform 名单的问题说明：即便修复了单点 bug，跨语言规则若没有统一事实源，后续仍会重新积累配置漂移风险。

## Root Cause
- 根因首先是关注点分布不均。最近几轮修复已经显著提升了编译器、配置校验和部分运行时路径的正确性，因此残余高风险自然向“运维生命周期、热加载一致性、隔离恢复”这些横切面集中；如果审查方法还停留在单函数正确性，很难主动看见这些问题。
- 更深层根因是多个子系统仍依赖隐含契约：
  - server 默认假设 engine/resources 可独立替换；
  - Lua pool 默认假设状态池只需 borrow/return；
  - `data_parallel` 默认假设 Transform 实现实例可并发重入；
  - column store 默认假设值切片足以表达输入存在性；
  - Python/Go 默认假设维护者会同步修改两份规则列表。
  这些假设并未全部写成显式能力、统一状态对象或单一事实源，因此只有做广域风险审计时才会集中暴露。
- 方法上也有一个正向反证：这次使用 `llmdoc:investigator` 的效果明显优于手工逐文件扫读，说明此前更大的短板不一定是“不会修”，而是“缺少足够系统的风险枚举方式”。先前 investigator 因 Bedrock 环境中 `model: opus` 不兼容而不可用，也客观限制了这种审计方法的使用；切换到 `model: sonnet` 后，广域调查能力才真正可用。

## Missing Docs or Signals
- 稳定文档目前更擅长描述 DAG、编译与配置边界，但对“热加载一致快照”“资源/engine 组合生命周期”“teardown 必须关闭 cgo/外部资源池”这类运维级不变量提示不足。
- `llmdoc/architecture/dag-engine.md` 或 server 相关文档中，应更明确区分“单字段原子替换”与“请求可观察配置快照一致性”不是同一件事。
- Lua 相关文档若后续稳定下来，值得补充两类信号：一是 pool 需要显式 teardown；二是 sandbox/状态复用若允许修改 baseline globals，就必须有完整恢复策略，而不仅是增量删除。
- `data_parallel` 当前 stable docs 已说明其运行时分流与 `common_output` 限制，但还缺少“算子实例可重入性”这一能力面；否则开发者容易把 Transform 默认视作可安全并发。
- 列存 DataFrame 文档可以补一条语义信号：列式实现优化不能改变“缺失字段 vs 显式 nil”语义，凡构造 `OperatorInput` 的路径都必须保留 `present` 信息。
- 更适合保留在 memory 的流程信号：针对成熟系统做 broad audit 时，优先怀疑 lifecycle / cleanup / isolation / global-state / multi-component snapshot 这类横切面，而不是只找算法性 bug。
- 另一个更适合保留在 memory 的信号是工具选择：`llmdoc:investigator` 很适合做跨目录深审；如果环境配置让该 agent 失效，应尽快修工具，而不是退回低效率的纯手工扫读。

## Promotion Candidates
- 适合后续提升到稳定文档：
  - 在 `llmdoc/architecture/dag-engine.md` 或相关 server 文档中补充热加载一致性原则：请求应观察到单次原子切换后的组合快照，涉及 engine 与 resources 时优先绑定成一个原子状态对象。
  - 在 Lua 运行时相关文档中补充 pool 生命周期与隔离恢复要求：pool 必须支持 teardown/close；状态复用必须恢复 baseline，而非只删除新增键。
  - 在 `data_parallel` 相关稳定文档中补充 operator reentrancy/capability 约束，明确不是所有 Transform 实例都天然可并发重入。
  - 在 column store / DataFrame 文档中补充 `present` bitmap 的语义地位，强调输入构造路径不得混淆 missing 与 explicit nil。
  - 在跨语言配置约定文档中补一条：凡 Python/Go 共用的规则枚举，优先单一事实源生成，避免双端手工维护。
- 更适合先留在 memory：
  - “高优先级残余风险集中在 lifecycle 而非核心算法，说明主流程在成熟、运维鲁棒性仍是下一阶段重点”属于阶段性判断，适合作为审计观察保留在 memory。
  - investigator agent 在 broad audit 中明显优于手工阅读、且其可用性受模型兼容性影响，这属于流程/工具经验，暂不需要写入架构文档。

## Follow-up
- 按优先级推进后续修复：先处理 hot-reload 一致快照、Lua pool `Close()` 生命周期、Lua baseline globals 恢复；随后评估 `data_parallel` capability 标记/串行回退与 `ColumnFrame.BuildInput` 的 present 语义修正。
- 若后续再次执行同类 broad audit，默认把检查入口放在 lifecycle、global state、teardown、snapshot consistency 与 cross-language single-source-of-truth 这五类横切面，并继续优先使用已修复为 `sonnet` 的 `llmdoc:investigator` 进行系统枚举。
