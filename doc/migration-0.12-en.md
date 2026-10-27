# Migration Guide: 0.11 → 0.12

0.12.0 aligns request contexts and cancellation semantics across the three runtimes (issue #217). This guide lists what downstream code must change, split into changes that break the build and changes that compile but behave differently. The full contract is in the "请求上下文与取消" (request context and cancellation) section of [`llmdoc/reference/operator-contract.md`](../llmdoc/reference/operator-contract.md).

Unaffected: JSON pipeline configs and `_PINEAPPLE_VERSION` (bumped with the version as usual), the Apple DSL and generated code, the HTTP wire format, `/stats` and `/dag` output, and ordinary requests with no cancellation or deadline — their results and error text are unchanged.

## 1. Build breaks: API signatures (Java / C++)

Go has no signature changes; it only adds `pine.CancelledError`. Go users can skip to section 2.

### Java

**Operator signature**: `CancellationToken` becomes `RequestContext`.

```java
// 0.11
public void execute(CancellationToken token, OperatorInput in, OperatorOutput out) throws PineErrors.OperatorException {
    if (token.isCancelled()) { ... }
}

// 0.12
public void execute(RequestContext ctx, OperatorInput in, OperatorOutput out) throws PineErrors.OperatorException {
    ctx.throwIfCancelled();          // or: if (ctx.isCancelled()) { ... }
}
```

**Resources**: `ResourceAware` is removed. Operators no longer receive an injected provider; they read it from the context in `execute`.

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
        ResourceProvider provider = ctx.resources();   // may be null
        ResourceProvider.GetResult r = provider.get("my_resource");
    }
}
```

`Engine.create(config, Engine.withResources(provider))` still works: the engine applies it when the context carries no provider.

**Engine callers**:

| 0.11 | 0.12 |
|---|---|
| `CancellationToken t = CancellationToken.create();` | `RequestContext.Cancelable ctx = RequestContext.withCancel(RequestContext.background());` |
| `t.cancel()` | `ctx.cancel()` (`Cancelable` is `AutoCloseable`; `close()` cancels, so try-with-resources works) |
| `engine.execute(t, common, items)` | `engine.execute(ctx, common, items)` |
| — | Deadline: `RequestContext.withTimeout(parent, Duration.ofMillis(200))` |
| — | Request-scoped value: `RequestContext.withValue(parent, KEY, value)`, read with `ctx.value(KEY)` |
| — | Per-request resources: `RequestContext.withResources(parent, provider)` |

The context-free `engine.execute(common, items)` and `PineServer.execute(common, items)` are unchanged; `PineServer` gains `execute(RequestContext, common, items)`.

**Other signatures**:

- `ParallelExecutor.execute(CancellationToken, ...)` → `execute(RequestContext, ...)`
- `RedisConnResource.runCommand(cmd, fn)` → `runCommand(ctx, cmd, fn)`; on a done context the command is not sent and `PineErrors.ContextException` is thrown

### C++

**Operator signature**:

```cpp
// 0.11
void execute(const pine::OperatorInput& in, pine::OperatorOutput& out) override;

// 0.12
void execute(const pine::RequestContext& ctx, const pine::OperatorInput& in, pine::OperatorOutput& out) override;
```

Custom operators marked `override` fail to compile, which makes them easy to find. If an operator does not use the context, name the parameter `const pine::RequestContext& /*ctx*/`.

**Resources**:

| 0.11 | 0.12 |
|---|---|
| Derive from `ResourceAware`, store the pointer in `set_resource_provider` | Remove; use `ctx.resource_provider()` in `execute` (may be `nullptr`) |
| `input.resources()` (data-resource map) | `ctx.resources()` |
| `frame.set_resources(&map)` | Removed; pass it with `RequestContext::with_resources(parent, &map)` |

`EngineOptions::resource_provider` still works: the engine applies it when the context carries no provider. The `Frame` virtual interface loses `set_resources` / `resources()`, so any `Frame` subclass must drop them as well.

**Engine callers**:

| 0.11 | 0.12 |
|---|---|
| `std::stop_source src;` | `auto ctx = pine::RequestContext::with_cancel(pine::RequestContext::background());` |
| `src.request_stop()` | `ctx.cancel()` (`Cancelable` is move-only and cancels on destruction) |
| `engine.execute(req, resources, src.get_token())` | `engine.execute(req, pine::RequestContext::with_resources(ctx, &resources))` |
| `engine.execute_traced_into(req, resources, &out, token)` | `engine.execute_traced_into(req, ctx, &out)` |
| — | Deadline: `pine::RequestContext::with_timeout(parent, std::chrono::milliseconds(200))` |
| — | Request-scoped value: `with_value(parent, &kMyKey, std::any(v))`, read with `ctx.value(&kMyKey)`; use the address of a private static object as the key |

The convenience overloads `execute(req)`, `execute(req, resources)`, `execute_traced(req, resources)` and `execute_traced_into(req, resources, out)` are unchanged.

**Build dependency**: libcurl 7.68+ is required. An older version fails the build with a message naming the required version.

## 2. Compiles, but behaves differently (all three runtimes, including Go)

### 2.1 Cancellation no longer reports success

0.11: after the caller cancelled (or its deadline passed), the remaining nodes were skipped and the run still returned success, with fields missing from the result.

0.12: if any node did not run because of the cancellation, and no operator failed on its own, the run returns a dedicated cancellation error. The partial result is still returned:

| | Error type | Cause |
|---|---|---|
| Go | `*pine.CancelledError` | `errors.Is(err, context.Canceled)` / `context.DeadlineExceeded` |
| Java | `Result.error` is `PineErrors.CancelledError` | `getCause()` returns `PineErrors.ContextException`; `isDeadlineExceeded()` tells the two apart |
| C++ | throws `pine::CancelledError` | `deadline_exceeded()`; `pine::error_as<pine::ContextError>(e)` |

The text is identical across runtimes: `pine: execution cancelled: context canceled` / `pine: execution cancelled: context deadline exceeded`.

Check the following:

- **Code that treats "no error" as "complete result"**: in 0.11 it silently received partial results; in 0.12 it sees this error. Decide whether retries, fallbacks and alerts should treat it differently from an ordinary failure (for example, no alert when the client already gave up).
- **C++ catch sites**: `pine::CancelledError` derives from `pine::Error`, **not** `pine::ExecutionError`. A caller that only catches `const pine::ExecutionError&` will miss it; catch `pine::Error` or `std::exception`.
- **Metrics**: these runs move from `pine_dag_executions_total{status="success"}` to `{status="error"}`, so error-rate dashboards rise and alert thresholds may need review.

Two cases that do not report `CancelledError`:

- The cancel lands while the last node is running, so no node was skipped: still a success.
- An operator itself returns or throws the context error (for example, an interrupted `transform_bench_sleep`): reported as an `ExecutionError` naming it, e.g. `pine: execution error in operator "X": context canceled`.

### 2.2 Skipped `data_parallel` shards fail the operator

0.11: shards that never ran because of cancellation were dropped, and only the finished shards were merged, silently returning output that covered only some items.
0.12: the operator fails with the context error, e.g. `pine: execution error in operator "X": context canceled`.

### 2.3 Built-in operators now honour cancellation (Java / C++; Go already did)

| Operator | 0.12 behaviour |
|---|---|
| `transform_bench_sleep` | Waits on the context; a cancel or passed deadline ends the wait and fails the operator |
| `transform_by_remote_pineapple` | Not sent if the context is done; an in-flight request is aborted. Text: `transform_by_remote_pineapple: request failed: Post "<url>": context canceled`; `fail_on_error` decides between a warning and an operator failure |
| `transform_redis_get` / `transform_redis_set` | No command is sent on a done context; handled like any Redis failure (get: cache miss + warning; set: write-failure warning; operator failure under `fail_on_error`). The per-command metric records `timeout` (deadline) or `error` (cancel) |
| `transform_by_lua` | Checks between items; a cancel fails the whole operator (`lua: item[<i>]: context canceled`). A single item already running is not interrupted |

### 2.4 Java / C++ server shutdown cancels in-flight requests

0.11: shutdown waited for in-flight requests to finish.
0.12: the in-flight requests' contexts are cancelled first, and those requests get a 500 response carrying the cancellation error. Deployments that rely on requests completing during shutdown should drain traffic before stopping the server. Go server shutdown is unchanged.

## 3. Known differences

These differences were reviewed and accepted, and are not treated as bugs:

- Lua cancellation text differs between runtimes (Go's two Lua backends already disagree with each other).
- A single Lua item cannot be interrupted while it runs (Java / C++).
- A Redis command already sent is not interrupted; only the socket timeouts bound it (Java / C++).
- Java remote calls can be cancelled only while waiting for response headers, not while reading the body.
- The Java server does not detect client disconnects, so a disconnect does not cancel the request (tracked in issue #220).

## Migration checklist

- [ ] Update Java / C++ custom operators to the new signature
- [ ] Operators that implemented `ResourceAware` now read from `ctx.resources()` / `ctx.resource_provider()`
- [ ] Engine calls using `CancellationToken` / `std::stop_token` now pass a `RequestContext`
- [ ] C++: make sure callers catch `pine::CancelledError`
- [ ] Review how retries, fallbacks and alerts handle the new cancellation error
- [ ] Review alert thresholds based on `pine_dag_executions_total{status="error"}`
- [ ] Java / C++ server: confirm the shutdown procedure accepts in-flight requests being cancelled
- [ ] C++: confirm libcurl ≥ 7.68
