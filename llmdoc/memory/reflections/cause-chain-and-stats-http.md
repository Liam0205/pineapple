---
name: cause-chain-and-stats-http
description: /stats.http 四方对齐与 cause-chain parity 复盘（d1abb43..9b7b851, 9 commits），记录跨语言状态辨识盲区、std::rethrow_if_nested footgun、R2 审计反向修正机制、cross-validate 第二种验证模式
type: reflection
---

# /stats.http 四方对齐 + cause-chain parity 复盘

## 任务背景

自上次 llmdoc 更新 commit `d1abb43` 至 `9b7b851`，共 9 个 commit，分属两个主题。

### 主题 A：/stats.http 四方对齐（4 commits）

| commit | 描述 |
|--------|------|
| `314510b` | pine-go：expose http request stats under /stats.http |
| `f482357` | pine-java：align /stats.http parity with pine-go |
| `6bf6194` | pine-python：align /stats.http parity with pine-go |
| `3372ced` | pine-cpp：align /stats.http parity with pine-go |

触发：用户指出 Section 13 中 Go/Java/Python 将内置 metrics 绑定在 Server Core 中做启动检验，而 C++ 的 HTTP layer 不注入默认的 http_metrics 路由中间件。

用户决策选择方案 D：/stats 新增 http 子树（双通道观测模型），采用 `map<string,...>` schema，sum_ns 使用 int64 纳秒。Go baseline 优先落地定义 schema，其余三方按 Go schema 对齐。

cross-validate Section 13 metrics parity 从 6 项扩展到 9 项（新增 [7] requests_total / [8] duration count / [9] schema shape）。

### 主题 B：cause-chain parity（5 commits）

| commit | 描述 |
|--------|------|
| `237f9f9` | pine-cpp：wire ExecutionError/PanicError into nested exception chain |
| `6d03c38` | pine-cpp：add error_as<T>/error_is<T> helpers for cause unwrap |
| `bda8054` | pine-python：restore ExecutionError cause chain |
| `4d7ba46` | cross-validate /stats.http across all four runtimes |
| `9b7b851` | cross-validate cause chain across four runtimes (Section 15) |

触发：编写 `.llmdoc-tmp/cpp-runtime-parity-audit-R2/00-summary.md`（诚实差异盘点）后，主线程重读 item 1"ExecutionError.Unwrap 合理差异"，发现 R2 误判。C++11 标准库的 `std::nested_exception` 完全对偶 Go 的 `errors.As`，因此原先标注的"合理差异"实为可修复差异。

同时发现 pine-python `ExecutionError.__init__(operator, message: str)` 只接收 string，丢失 inner exception。issue #34 已开并由 commit `bda8054` 关闭。

---

## Expected vs Actual

### 主题 A

- **预期**：用户认为 Go/Java/Python 三方已 default-on http metrics，仅 C++ 落后。
- **实际**：辨识后发现 Java 仍是 conditional（`if (metricsProvider != null)` 守卫），Python 完全没有 metrics Provider 抽象也没有 http_metrics middleware。四方状态各不相同，差异远比用户描述的大。

### 主题 B

- **预期**：R2 审计标注的"ExecutionError.Unwrap 合理差异"应当被接受，cause chain 在 C++ 不可对偶。
- **实际**：`std::nested_exception` 完全可以对偶 Go `errors.As`。R2 审计结论错误。诚实差异盘点倒逼了审计结果的自我纠正。

---

## What Went Wrong

### 1. 跨语言状态辨识盲区

用户原话以为"Go/Java/Python 都 default-on，只 C++ 落后"。直接采信这一描述会导致只修 C++ 一方。实际 grep + 代码读核实后，四方状态各异：Go default-on、Java conditional、Python 完全缺失、C++ working tree 半改。

### 2. R2 审计"合理差异"的误判

R2 审计时将"C++ 没有 errors.As 等价能力"标为合理差异。根因是审计者对 C++11 标准库掌握不够精确：`std::nested_exception` 在 C++11 即可用，`std::rethrow_if_nested` 提供递归 unwrap 能力，完全对偶 Go 的 `errors.As`。

### 3. std::rethrow_if_nested 标准库 footgun

`std::rethrow_if_nested(e)` 在 e 是 `nested_exception` 派生但 `nested_ptr() == nullptr` 时，仍会调用 `rethrow_nested()`，导致 `std::terminate`。首个 doctest case "ExecutionError thrown without nested cause" 触发 SIGABRT。修复方式：在 `walk_nested` 中显式检查 `nested_ptr()` 非 null 再重抛。

### 4. worker agent 启动失败

主题 A 尝试用 worker agent 并行启动 Java/Python/cpp 三方落地，但 worker agent 因 `xhigh` effort 配置失败，主线程亲自接手完成。复杂跨语言改造中 worker agent 的启动失败导致额外的回滚和上下文切换成本。

---

## Root Cause

### 辨识盲区

用户描述差异时使用了概括性判断（"其它三方都是 default-on"），但实际各运行时的实现进度不同步。如果在动手前没有先用 grep 核实四方真实状态，就会基于错误前提做改造计划。

### R2 误判

审计流程缺少"审计结论发布后二次检验"环节。R2 写出"合理差异"结论时没有附上标准库对偶检索的证据链。直到写诚实差异盘点 summary 时，才因为要白纸黑字写下"为什么不可对偶"而发现理由站不住脚。

### std::rethrow_if_nested

C++ 标准库的设计意图是"若无 nested，则不做任何事"，但实现行为是"若继承了 nested_exception 但 nested_ptr() 为 null，照样 rethrow 并 terminate"。这是一个众所周知的 C++ footgun，但审计和实现阶段都未事先识别。

---

## 关键决策与机制演进

### 1. 诚实差异盘点催生审计自纠正

审计完成后写"差异清单"文档，把"合理差异"的理由逐条白纸黑字列出。这个过程本身暴露了理由的薄弱之处。

结论：审计完成后必须做一轮"反向审计"，对每个"accepted difference"写出明确的技术理由。如果理由只是"好像不行"级别的含糊判断，该项应标记为 pending verification 而非 accepted。

### 2. Cross-validate 第二种验证模式

之前 Section 1-14 全部是 fixture-driven HTTP 字节对比（第一种模式）。Section 15 首次使用"四方各跑一个 probe binary，统一 stdout 字符串比对"的方式，覆盖"语言层 API 形态"差异（cause chain 不在 HTTP 接口可见，JSON 序列化为 string 已字节级一致，但语言层 unwrap 能力是否可用需要独立探针）。

这标志着 cross-validate 框架从"HTTP 接口行为对比"扩展到"运行时内部 API 能力对比"。

### 3. /stats.http schema 固化

Go baseline 定义 schema：
- `requests_total`: `map<"METHOD path bucket", int64>`
- `request_duration_seconds`: `map<"METHOD path", {count: int64, sum_ns: int64}>`

Section 13 metrics parity 新增 3 项检查（[7] requests_total / [8] duration count / [9] schema shape），从字段值校验扩展到 schema shape 校验。

### 4. issue #34 模板

issue #34（pine-python cause chain 丢失）精确捕获了"语言原生能力可达却未达"这种 P3 级差异。issue 描述模板：标明语言原生能力（Python `__cause__`）、当前实现状态（只接收 string）、对等目标（Go `fmt.Errorf("%w", inner)`），可复用于同类发现。

---

## Missing Docs or Signals

### 稳定文档缺口

1. `reference/metrics-observability.md` 缺少 /stats.http 子树的 schema 定义与四运行时实现锚点。
2. `architecture/pine-cpp-runtime.md` 缺少 cause chain（`std::nested_exception` + `walk_nested` + `error_as<T>`）的描述。
3. `must/conventions.md` 中跨运行时对齐的审计流程缺少"accepted difference 必须附技术证据链"的约定。
4. cross-validate 框架的两种验证模式（HTTP fixture 对比 vs probe binary stdout 对比）未在 `guides/cross-layer-validation.md` 中记录。

### 仅保留在 memory

- `std::rethrow_if_nested` 的 null nested_ptr footgun 及 `walk_nested` 的显式检查实现细节。
- worker agent `xhigh` effort 配置失败的具体错误信息。
- issue #34 的具体描述文本。
- 各 commit 的 diff 细节。

---

## Promotion Candidates

### 应提升到 `reference/metrics-observability.md`
- /stats.http 子树 schema（requests_total / request_duration_seconds）。
- 四运行时 http stats 实现文件锚点。

### 应提升到 `architecture/pine-cpp-runtime.md`
- cause chain 基础设施：`std::nested_exception` 对偶 Go `errors.As`、`walk_nested` helper、`error_as<T>` / `error_is<T>` 模板。

### 应提升到 `must/conventions.md`
- 审计"accepted difference"必须附上标准库/语言规范级别的技术证据链；含糊理由应标为 pending verification。
- 用户描述跨语言差异时，必须先 grep + 代码读核实四方真实状态再制定改造计划。

### 应提升到 `guides/cross-layer-validation.md`
- Cross-validate 第二种验证模式：probe binary stdout 对比（Section 15），覆盖不经过 HTTP 接口的运行时内部 API 能力差异。
- Section 13 扩展到 9 项检查（含 schema shape 校验）。

---

## Follow-up

1. recorder agent 更新 `reference/metrics-observability.md`，补 /stats.http schema 与四运行时实现锚点。
2. recorder agent 更新 `architecture/pine-cpp-runtime.md`，补 cause chain 描述。
3. recorder agent 更新 `guides/cross-layer-validation.md`，补第二种验证模式（probe binary stdout 对比）与 Section 15 说明。
4. 在 `must/conventions.md` 中补"accepted difference 必须附技术证据链"约定。
5. R3 审计入口建议：优先审计 `.llmdoc-tmp/cpp-runtime-parity-audit-R2/00-summary.md` 中剩余的"accepted difference"项，逐一按本次教训要求附技术证据链或重新标为 pending。

---

## 接下来类似工作可改进的地方

1. **差异辨识前置核实**：用户描述"X 方已实现、Y 方缺失"时，不要直接信。建立固定步骤：先用 grep 在四个运行时目录中搜索关键函数/类名，用 10 分钟核实四方真实状态，再制定改造计划。这比后期发现 Java conditional / Python 完全缺失时的返工成本低得多。

2. **审计"合理差异"附证据链**：每个标为 accepted 的差异项，必须附上"为什么目标语言做不到"的标准库/语言规范级引用。如果只能写出"好像不行"级别的理由，立即标为 pending verification，避免 R2 级别的误判在下一阶段才被发现。

3. **复杂跨语言改造直接主线程做**：涉及四运行时同步改造的任务，worker agent 的启动/配置失败会导致额外回滚成本。当改造涉及 schema 定义 + 四方实现 + cross-validate 测试的组合时，主线程连续完成比 worker 并行尝试更可控。
