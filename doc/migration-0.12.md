# 迁移指南：0.11 → 0.12

0.12.0 统一了三个运行时的请求上下文与取消语义（issue #217）。本文列出下游需要做的改动，按「编译报错」和「编译通过但行为变了」两类分开。完整契约见 [`llmdoc/reference/operator-contract.md`](../llmdoc/reference/operator-contract.md) 的「请求上下文与取消」。

不受影响的部分：JSON pipeline 配置与 `_PINEAPPLE_VERSION`（照常随版本号更新）、Apple DSL 与代码生成产物、HTTP 协议格式、`/stats` 与 `/dag` 输出，以及没有取消、没有截止时间的普通请求——它们的结果与错误文案都不变。

## 一、编译报错：API 签名（Java / C++）

Go 没有签名变化，只新增了 `pine.CancelledError`，可直接跳到第二节。

### Java

**算子签名**：`CancellationToken` 换成 `RequestContext`。

```java
// 0.11
public void execute(CancellationToken token, OperatorInput in, OperatorOutput out) throws PineErrors.OperatorException {
    if (token.isCancelled()) { ... }
}

// 0.12
public void execute(RequestContext ctx, OperatorInput in, OperatorOutput out) throws PineErrors.OperatorException {
    ctx.throwIfCancelled();          // 或 if (ctx.isCancelled()) { ... }
}
```

**资源**：删除了 `ResourceAware`，算子不再接收注入，改在 execute 里从上下文取。

```java
// 0.11
public class MyOp extends AbstractOperator implements ResourceAware {
    private ResourceProvider provider;
    @Override public void setResourceProvider(ResourceProvider p) { this.provider = p; }
    @Override public void execute(CancellationToken token, OperatorInput in, OperatorOutput out) {
        ResourceProvider.GetResult r = provider.get("my_resource");
    }
}

// 0.12
public class MyOp extends AbstractOperator {
    @Override public void execute(RequestContext ctx, OperatorInput in, OperatorOutput out) {
        ResourceProvider provider = ctx.resources();   // 可能为 null
        ResourceProvider.GetResult r = provider.get("my_resource");
    }
}
```

`Engine.create(config, Engine.withResources(provider))` 仍然可用：上下文没有携带 provider 时，引擎用它补上。

**引擎调用方**：

| 0.11 | 0.12 |
|---|---|
| `CancellationToken t = CancellationToken.create();` | `RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());` |
| `t.cancel()` | `ctx.cancel()`（`Cancelable` 是 `AutoCloseable`，`close()` 即取消，可放进 try-with-resources） |
| `engine.execute(t, common, items)` | `engine.execute(ctx, common, items)` |
| — | 截止时间：`RequestContext.withTimeout(parent, Duration.ofMillis(200))` |
| — | 请求级值：`RequestContext.withValue(parent, KEY, value)`，读取 `ctx.value(KEY)` |
| — | 按请求注入资源：`RequestContext.withResources(parent, provider)` |

不带上下文的 `engine.execute(common, items)` 与 `PineServer.execute(common, items)` 没有变化；`PineServer` 新增了 `execute(RequestContext, common, items)`。

**其他签名**：

- `ParallelExecutor.execute(CancellationToken, ...)` → `execute(RequestContext, ...)`
- `RedisConnResource.runCommand(cmd, fn)` → `runCommand(ctx, cmd, fn)`；上下文已结束时不发送命令，直接抛 `PineErrors.ContextException`

### C++

**算子签名**：

```cpp
// 0.11
void execute(const pine::OperatorInput& in, pine::OperatorOutput& out) override;

// 0.12
void execute(const pine::RequestContext& ctx, const pine::OperatorInput& in, pine::OperatorOutput& out) override;
```

带 `override` 的自定义算子会直接编译报错，可以据此找全。不使用上下文时把参数写成 `const pine::RequestContext& /*ctx*/`。

**资源**：

| 0.11 | 0.12 |
|---|---|
| 继承 `ResourceAware`，在 `set_resource_provider` 里保存指针 | 删掉；在 execute 里用 `ctx.resource_provider()`（可能为 `nullptr`） |
| `input.resources()`（数据型资源 map） | `ctx.resources()` |
| `frame.set_resources(&map)` | 删除；用 `RequestContext::with_resources(parent, &map)` 传入 |

`EngineOptions::resource_provider` 仍然可用：上下文没有携带 provider 时，引擎用它补上。`Frame` 虚接口删掉了 `set_resources` / `resources()` 两个方法，继承过 `Frame` 的实现需要一并删掉。

**引擎调用方**：

| 0.11 | 0.12 |
|---|---|
| `std::stop_source src;` | `auto ctx = pine::RequestContext::with_cancel(pine::RequestContext::background());` |
| `src.request_stop()` | `ctx.cancel()`（`Cancelable` 只能移动不能拷贝，析构时自动取消） |
| `engine.execute(req, resources, src.get_token())` | `engine.execute(req, pine::RequestContext::with_resources(ctx, &resources))` |
| `engine.execute_traced_into(req, resources, &out, token)` | `engine.execute_traced_into(req, ctx, &out)` |
| — | 截止时间：`pine::RequestContext::with_timeout(parent, std::chrono::milliseconds(200))` |
| — | 请求级值：`with_value(parent, &kMyKey, std::any(v))`，读取 `ctx.value(&kMyKey)`；key 用私有静态对象的地址 |

`execute(req)`、`execute(req, resources)`、`execute_traced(req, resources)`、`execute_traced_into(req, resources, out)` 这几个便捷接口没有变化。

**构建依赖**：需要 libcurl 7.68+。版本不够时会编译报错并指出所需版本。

## 二、编译通过但行为变了（三个运行时都有，含 Go）

### 1. 取消不再报成功

0.11：调用方取消（或截止时间到）后，剩下的节点被跳过，执行仍然返回成功，结果里缺字段。

0.12：只要有节点因此没有运行、且没有算子自己失败，就返回专用取消错误，部分结果仍然保留：

| | 错误类型 | 取原因 |
|---|---|---|
| Go | `*pine.CancelledError` | `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded` |
| Java | `Result.error` 为 `PineErrors.CancelledError` | `getCause()` 返回 `PineErrors.ContextException`，`isDeadlineExceeded()` 区分 |
| C++ | 抛出 `pine::CancelledError` | `deadline_exceeded()`；`pine::error_as<pine::ContextError>(e)` |

文案三方一致：`pine: execution cancelled: context canceled` / `pine: execution cancelled: context deadline exceeded`。

需要检查的地方：

- **用「没有 error」判断结果完整的代码**：0.11 会悄悄拿到残缺结果，0.12 会看到这个错误。检查重试、降级、告警是否要把它和普通执行失败区别对待（例如客户端已放弃时不必告警）。
- **C++ 的 catch**：`pine::CancelledError` 继承 `pine::Error`，**不是** `pine::ExecutionError`。只 `catch (const pine::ExecutionError&)` 的调用方会漏掉它，应 catch `pine::Error` 或 `std::exception`。
- **指标**：这类执行从 `pine_dag_executions_total{status="success"}` 改记为 `{status="error"}`，错误率看板会上升，告警阈值可能需要复核。

两种不报 `CancelledError` 的情况：

- 取消落在最后一个节点运行期间，没有节点被跳过：仍是成功。
- 算子自己返回或抛出上下文错误（例如被打断的 `transform_bench_sleep`）：报带算子名的 `ExecutionError`，如 `pine: execution error in operator "X": context canceled`。

### 2. `data_parallel` 分片被跳过时算子失败

0.11：有分片因取消没有运行时，只合并跑完的分片，静默返回只覆盖部分 item 的输出。
0.12：该算子以上下文错误失败，例如 `pine: execution error in operator "X": context canceled`。

### 3. 内置算子开始响应取消（Java / C++；Go 原本如此）

| 算子 | 0.12 行为 |
|---|---|
| `transform_bench_sleep` | 等待在上下文上，取消或截止时间到即结束并报错 |
| `transform_by_remote_pineapple` | 上下文已结束则不发请求；在途请求会被中止。文案 `transform_by_remote_pineapple: request failed: Post "<url>": context canceled`，受 `fail_on_error` 控制是 warning 还是算子失败 |
| `transform_redis_get` / `transform_redis_set` | 上下文已结束则不发命令，当作 Redis 失败处理（get：cache miss + warning；set：写失败 warning；`fail_on_error` 下算子失败）。per-command 指标记为 `timeout`（截止时间）或 `error`（取消） |
| `transform_by_lua` | 在两个 item 之间检查，被取消时整个算子失败（`lua: item[<i>]: context canceled`）；单个 item 执行中途不中断 |

### 4. Java / C++ server 停机会取消在途请求

0.11：停机时等在途请求跑完。
0.12：先取消在途请求的上下文，这些请求会收到带取消错误的 500 响应。依赖「停机时请求能正常跑完」的部署需要调整，比如先摘流量再停机。Go server 的停机行为没有变化。

### 5. 去重 merge 改写 `_source` 恢复可用（0.12.2）

0.11.0～0.12.1：去重 merge 用 `SetItem` 把合并后的来源标签写回 `_source` 时，报 `output contract violation: operator wrote undeclared item output field(s) [_source]`，而 DSL 又不允许把 `_source` 声明进 `item_output`。
0.12.2：实现了 `MutatesRowSet`、且 `item_input` 含 `_source` 的算子可以改写已有 item 的 `_source`，不需要声明。算子写的其他字段仍要声明在 `item_output` 里。

## 三、已知差异

以下差异经评估后接受，不作为缺陷处理：

- Lua 取消文案三方不同（Go 的两个 Lua 后端本身就不一致）。
- Lua 单个 item 执行中途无法中断（Java / C++）。
- 已经发出的 Redis 命令不会被中断，只受 socket 超时约束（Java / C++）。
- Java remote 只有等待响应头阶段可取消，读响应体阶段不可取消。
- Java server 不感知客户端断连，断连不会取消请求（跟踪见 issue #220）。

## 迁移检查清单

- [ ] Java / C++ 自定义算子改为新签名
- [ ] 实现了 `ResourceAware` 的算子改为从 `ctx.resources()` / `ctx.resource_provider()` 读取
- [ ] 使用 `CancellationToken` / `std::stop_token` 调用引擎的地方改用 `RequestContext`
- [ ] C++：确认调用方能 catch 到 `pine::CancelledError`
- [ ] 检查重试、降级、告警对新取消错误的处理
- [ ] 复核基于 `pine_dag_executions_total{status="error"}` 的告警阈值
- [ ] Java / C++ server：确认停机流程能接受在途请求被取消
- [ ] C++：确认 libcurl ≥ 7.68
