---
name: audit-extensibility-blindspot
description: Parity 审计的结构性盲点：只验证已有功能的输入输出，未验证 API 对下游开发范式的约束是否一致
type: reflection
---

## 事件

Pine-Java 经过 19 轮 Go-parity 审计、11 层交叉验证后宣告收敛（约 90 项差异关闭）。但下游项目 tipsy-recsys 发现 Java 的 PineServer 无法支持自定义 HTTP endpoint——Go 的 middleware 可以拦截任意路径，而 Java 的 HttpServer 仅路由到已注册 context，未注册路径在 middleware 介入前即返回 404。

这意味着：Go 引擎的用户可以通过 middleware 扩展任意 endpoint（如 `/recommend`），Java 引擎的用户则不能。两者功能输出完全一致，但对下游施加的**开发范式约束**不同。

## 根因分析：思维结构缺失

四层认知缺陷叠加：

1. **审计问题框架错误**：19 轮审计全部在问"输出 X 是否一致？"——比较 API 产出物。从未问"下游能否做 Y？"——比较 API 所赋能的集成模式。
2. **交叉验证只测已知路径**：`/health`、`/execute`、`/stats`、`/dag` 全部是正空间。从未测试**未注册路径**的行为——负空间（unknown path → middleware 是否可见）。
3. **心智模型是"函数等价"而非"能力等价"**：同输入→同输出是应用程序的正确性标准。基础设施的正确性还必须包含"消费者可用的集成模式是否等价"。
4. **根本原因**：将基础设施当应用程序审计。应用程序的正确性是其输出；基础设施的正确性还包括它对构建于其上的业务所施加的开发范式。

## 对审计方法论的修正

| 维度 | 旧方法 | 修正后 |
|------|--------|--------|
| 审计对象 | 已注册 API 的输入/输出 | + 未注册路径的行为、middleware 可达性、扩展点暴露方式 |
| 测试空间 | 正空间（已知端点） | + 负空间（未知端点、未注册路径、边界外请求） |
| 等价定义 | 函数等价（same input → same output） | 能力等价（same integration patterns available） |
| 审计视角 | 仓库内可见行为 | + 对下游开发范式的约束是否一致 |

具体检查项补充：
- 请求生命周期中 middleware 的介入时机是否跨引擎一致
- 未注册路径是否在 middleware 层可见（而非被路由层提前拒绝）
- Server 的扩展点（添加 handler、注入 middleware、自定义路由）API 是否跨引擎对等

## 提升为稳定文档的候选项

### 应提升到 `must/conventions.md`
- **基础设施审计必须包含能力等价维度**：跨引擎 parity 审计不仅验证已知 API 的输出一致性，还需验证扩展点、middleware 可达性、未注册路径行为等"开发范式约束"的一致性。

### 应提升到 `guides/cross-layer-validation.md`
- 新增"负空间验证"层：对未注册路径发送请求，断言 middleware 可见性与响应行为跨引擎一致。
- 新增"扩展点对等"检查：枚举 Server 级扩展点（add handler、wrap middleware、custom route），验证三引擎均暴露等价能力。

### 仅保留在 memory
- Java HttpServer 的具体修复方案（路由层 fallback 到 middleware chain 或切换到 path-prefix 匹配模式）
- 本次事件的时间线与发现经过

## Follow-up
1. 为 cross-validate 添加"未注册路径 + middleware 可见性"测试用例。
2. 在 `conventions.md` 中补充"能力等价"审计维度。
3. 修复 Java PineServer 使未注册路径对 middleware 可见（具体方案待设计）。
