---
title: "Pineapple v0.4.0 → v0.5.0：从测试加固到 review 驱动的质量闭环"
date: 2026-04-23
categories: Algorithm and Computer Science
tags:
  - Pipeline Engine
  - Go
  - Python
  - Code Review
  - Codegen
  - Metrics
  - Testing
---

本文是「Pineapple」系列的第三篇，[上一篇](/2026/04/22/from-visualization-to-data-parallelism/)记录了从可视化到数据并行的十次迭代。这一轮迭代从 v0.4.0 到 v0.5.0，节奏明显不同于前两轮——不再是密集的功能堆叠，而是围绕「质量」展开：补测试、系统化错误处理、接入生产监控、以及一次 code review 驱动的三连修复。这篇文章记录每个改动的背景、技术决策和收效。

<!-- more -->

## Apple DSL 编译期 data_parallel 校验

上一轮实现了算子级数据并行框架，Go 引擎在 `NewEngine` 加载 JSON 配置时会校验两条约束：`data_parallel > 1` 只允许 Transform 算子，且 `common_output` 必须为空。但 Apple/Python DSL 编译器没有同步跟进这两条校验——用户写了不合法的 Python pipeline，`flow.compile()` 能正常输出 JSON，要等到 Go 加载时才报错。

问题在于反馈链路太长。Python 编译阶段和 Go 加载阶段之间可能隔着 CI pipeline、配置文件传输、服务重启等多个环节。用户写完代码运行 `python3 demo.py` 看到成功输出，以为万事大吉——直到服务重启时 Go 拒绝了配置，这时候要回头排查就不那么直觉了。

修复是在 `apple/validator.py` 中镜像 Go 侧的校验逻辑：

```python
def validate_data_parallel(ops: list[tuple[str, OpCall]]) -> None:
    for name, op in ops:
        if op.data_parallel > 1:
            if not op.type_name.startswith("transform_"):
                raise ValidationError(
                    f"operator {name!r}: data_parallel={op.data_parallel} "
                    f"is only supported for Transform operators"
                )
            if op.common_output:
                raise ValidationError(
                    f"operator {name!r}: data_parallel={op.data_parallel} "
                    f"requires empty common_output"
                )
```

`compile_flow` 中的校验调用链变为：字段覆盖 → 写后覆写 → `data_parallel` 约束 → 死代码检测。现在用户在 `flow.compile()` 这一步就能拿到明确的错误提示。

这里的技术决策逻辑很清晰：**Apple 编译期校验和 Go 加载期校验是两层守门**。Apple 校验服务于快速反馈（用户运行 Python 脚本时立刻报错），Go 校验服务于防御纵深（拦截手写 JSON 或绕过 DSL 的配置源）。两层校验的规则应该保持同步——任何在 Go 侧新增的编译期约束，都应该同步到 Apple 侧。

## 测试覆盖率：从 70.5% 到 77.6%

之前的开发重心在功能实现，测试主要覆盖核心路径。两块短板很明显：`pkg/server` 的 HTTP handler 覆盖率只有 10.6%，`operators/transform` 中 Redis 算子覆盖率只有 56.6%。

### HTTP handler 测试

使用标准库的 `net/http/httptest` 直接测试 handler 函数，不启动真实 HTTP 服务：

```go
func TestExecuteHandler(t *testing.T) {
    engine, _ := pine.NewEngine(testConfig)
    handler := makeExecuteHandler(&engine)

    body := `{"common":{"user_age":16},"items":[]}`
    req := httptest.NewRequest("POST", "/execute", strings.NewReader(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()
    handler(w, req)

    if w.Code != 200 {
        t.Errorf("status = %d, want 200", w.Code)
    }
}
```

14 个 handler 测试覆盖了正常请求、无效 JSON、空 body、错误 HTTP 方法、engine 未加载等场景。覆盖率从 10.6% 跳到 52.3%。

### Redis 算子测试

Redis 算子（`transform_redis_get` / `transform_redis_set`）依赖外部 Redis 实例，之前的少量测试靠跳过（`t.Skip`）或者连接本地 Redis。引入 [miniredis](https://github.com/alicebob/miniredis) 后，测试在内存中运行完整的 Redis 协议，无外部依赖：

```go
func TestRedisGetBasic(t *testing.T) {
    s := miniredis.RunT(t)

    s.Set("prefix:item_a", `{"score":0.95}`)

    op := &RedisGetOp{}
    op.Init(map[string]any{
        "redis_addr":     s.Addr(),
        "redis_password": "",
        "redis_db":       int64(0),
        "key_prefix":     "prefix:",
        "data_type":      "json",
    })
    // ... execute and verify
}
```

21 个测试覆盖了 string/json/hash 三种数据类型、key 缺失、连接错误、TTL 过期等场景。覆盖率从 56.6% 提升到 88.8%。

测试的价值不仅在于覆盖率数字本身。这一轮补测试的过程中，发现了 `transform_size` 算子的一个边界条件没有被覆盖，以及 Redis set 算子在 TTL=0 时的行为不符合直觉（应该是永不过期，但代码路径不同）——都在补测试的过程中顺手修正了。

## 错误处理系统化

上一轮迭代中，各处错误处理是逐个功能点随手写的，缺乏系统视角。`design_doc/07_error_handling.md` 原本只有寥寥几行原则，不够用了。

这次重写将错误处理从五个维度系统化：

**类型化错误**——Pine 定义了 5 种结构化错误类型（`ConfigError`、`RegistryError`、`ValidationError`、`ExecutionError`、`PanicError`），调用方通过 `errors.As` 精确判断错误来源和阶段。`ExecutionError` 和 `PanicError` 携带 `Operator` 字段标识出错算子。

**分级处理**——可恢复错误（算子自行降级 + warning）、不可恢复错误（终止 DAG）、panic（recover + 终止 DAG，绝不 crash 进程）。`fail_on_error` 模式让业务团队按下游服务重要程度灵活选择。

**Warning 传播路径**——从算子 `output.SetWarning(err)` 到 scheduler 收集，到 `Engine.Execute` 返回，到 HTTP handler 序列化。全链路 mutex 保护，并发安全。

**DAG 中止机制**——`sync.Once` 保证首个 fatal error 只记录一次，`cancel()` 传播到所有正在执行的算子。未调度的下游算子不启动。trace 仅包含实际执行过的算子，不包含因中止而跳过的。

**HTTP 错误映射**——引擎错误到 HTTP 状态码的确定性映射，即使 500 也返回部分结果便于调试。

这个文档本身不涉及代码改动，但它的意义在于：把散落在各处的错误处理决策收敛为一个可引用的规范。后续新增算子或改动错误路径时，不需要到处翻代码确认「这里应该 return err 还是 SetWarning」——查文档就行。

## 可插拔 Metrics Provider

Pineapple 之前已有 `/stats` 端点，基于 atomic 计数器，零外部依赖。但生产环境需要接入 Prometheus 或类似系统做告警和看板。

### 接口设计

`pkg/metrics` 定义了三种指标原语：

```go
type Provider interface {
    NewCounter(opts MetricOpts) Counter
    NewGauge(opts MetricOpts) Gauge
    NewHistogram(opts HistogramOpts) Histogram
}
```

关键决策：**Pineapple 核心库不依赖 `prometheus/client_golang`**。Provider 接口是 Pineapple 定义的，Prometheus 适配器由用户在自己的项目中实现。这意味着 Pineapple 的 `go.mod` 不会引入 Prometheus 的传递依赖——对于一个库来说，这很重要。

### 双通道记录

引擎内部同时向两个通道写入指标：

1. **atomic 计数器**——驱动 `/stats` JSON 端点，始终可用，零配置。
2. **Provider 接口**——驱动外部导出（Prometheus 等），默认是零开销的 Nop 实现。

两个通道完全独立。即使 Provider 没有注入，`/stats` 照常工作；即使 Provider 注入了，`/stats` 不受影响。这避免了「接入了 Prometheus 就得靠 Prometheus 才能看指标」的锁定。

### 指标覆盖

三个维度的指标：

- **调度器级**：DAG 执行总次数、当前活跃算子数、per-operator 执行/跳过/错误计数和耗时分布
- **Lua pool 级**：per-operator 的 state 借出/归还/创建计数和当前活跃数
- **配置热重载级**：重载成功/失败计数和耗时分布

### Nop 的零成本保证

当未注入 Provider 时，所有 metrics 调用落到 Nop 实现：

```go
type nopCounter struct{}
func (nopCounter) With(...string) Counter { return nopCounter{} }
func (nopCounter) Inc()                   {}
```

Go 编译器会将这些方法内联为空操作。在热路径上调用 `counter.Inc()` 的开销趋近于零——不需要 feature flag 或 if 判断。

## Review 驱动的三连修复

一位朋友对 `transform_resource_lookup` 相关代码做了 review，提出三个问题。逐一分析后确认都成立——两个是真 bug，一个是编译期校验缺失。

### P2：非 string lookup key 的静默失败

`resource_lookup.go` 中原始的 key 读取：

```go
key, _ := in.Item(i, o.lookupKey).(string)
```

Go 的 type assertion 失败时返回零值——对 `string` 就是 `""`。如果 item 的 key 字段是 JSON 数字（反序列化为 `float64`），断言静默失败，`key` 变成空字符串，查表永远 miss。如果配置了 `default_value`，miss 后会写入默认值——用户拿到的是「看起来正确但其实完全错误」的结果。

这类 bug 特别危险，因为它不会报错、不会 panic、不会 warning，pipeline 正常跑完，结果看起来合理但数值全错。

修复方案是显式类型分派 + nil 处理：

```go
raw := in.Item(i, o.lookupKey)
if raw == nil {
    if o.hasDefault {
        out.SetItem(i, o.outputField, o.defaultValue)
    }
    continue
}
var key string
switch v := raw.(type) {
case string:
    key = v
case float64:
    if v == float64(int64(v)) {
        key = strconv.FormatInt(int64(v), 10)
    } else {
        key = strconv.FormatFloat(v, 'f', -1, 64)
    }
default:
    key = fmt.Sprintf("%v", raw)
}
```

`float64` 分支里的整数判断（`v == float64(int64(v))`）是关键：JSON 数字 `1` 在 Go 中是 `float64(1.0)`，对应的 map key 应该是 `"1"` 而不是 `"1.000000"`。只有真正的小数才走 `FormatFloat`。

### P3：codegen 无条件序列化 `default_value=None`

Python codegen 为 `transform_resource_lookup` 生成的 wrapper 长这样：

```python
def __call__(self, *, default_value: Any = None, ...):
    return self._apply(
        params={
            "default_value": default_value,
            ...
        },
    )
```

用户不传 `default_value` 时，Python 的 `None` 被无条件序列化为 JSON `null`。Go 侧的 `Init` 函数看到 `params["default_value"]` 这个 key 存在，就设置 `hasDefault = true`——于是所有查表 miss 的 item 都被写入 `null`，而不是跳过。

这个 bug 的链路跨越了四层：Python 默认值 → JSON 序列化 → Go params 解析 → 运行时行为。每一层单独看都「没错」——Python 的 `None` 确实应该序列化为 `null`，Go 侧检查 key 存在也是合理的 Init 逻辑。但串联起来就是一个语义错误。

修复在 codegen 模板层，将参数分为两类：

```go
// alwaysParams: required 或有 Default 的参数——总是写入 _params
func alwaysParams(params map[string]types.ParamSpec) []string { ... }

// conditionalParams: optional 且无 Default 的参数——只在 is not None 时写入
func conditionalParams(params map[string]types.ParamSpec) []string { ... }
```

模板改为：

```python
_params = {
    "lookup_key": lookup_key,
    "output_field": output_field,
    "resource_name": resource_name,
}
if default_value is not None:
    _params["default_value"] = default_value
```

这个修复不是只针对 `transform_resource_lookup` 的补丁，而是模板层的系统性变更。所有 optional 且无 Default 的参数都走条件写入——`transform_by_remote_pineapple` 的 `common_request`、`item_response` 等四个 optional `"any"` 参数也自动受益。

### P1：Apple 编译期参数-元数据一致性校验

`transform_resource_lookup` 有一个特殊的属性：它的业务参数 `lookup_key` 和 `output_field` 隐含了对元数据的要求——`lookup_key` 指定的字段必须出现在 `item_input` 中（否则运行时读不到），`output_field` 必须出现在 `item_output` 中（否则下游看不到写入的结果）。

但 Apple 编译器之前不做这个校验。用户可以写出：

```python
flow.transform_resource_lookup(
    resource_name="features",
    lookup_key="item_id",
    output_field="item_feature",
    item_input=[],         # 遗漏了 item_id
    item_output=["feat"],  # output_field 和 item_output 不匹配
)
```

配置能编译，Go 也能加载（因为 DAG builder 只看 `$metadata`，不看业务参数），但运行时 `BuildInput` 不会为这个算子构造 `item_id` 字段，lookup 变成 silent no-op。

修复方案是在 `apple/validator.py` 中新增规则表驱动的校验：

```python
_PARAM_METADATA_RULES: dict[str, list[tuple[str, str]]] = {
    "transform_resource_lookup": [
        ("lookup_key", "item_input"),
        ("output_field", "item_output"),
    ],
}

def validate_param_metadata_consistency(ops):
    for name, op in ops:
        rules = _PARAM_METADATA_RULES.get(op.type_name)
        if not rules:
            continue
        for param_name, metadata_attr in rules:
            value = op.params.get(param_name)
            if value and value not in getattr(op, metadata_attr, []):
                raise ValidationError(...)
```

规则表的设计是可扩展的——未来如果有其他算子也存在「业务参数隐含元数据要求」的情况，只需往 `_PARAM_METADATA_RULES` 中添加条目即可。

## 小结

这一轮迭代的主题是「质量」，但更准确地说，是三种不同层次的质量保障机制在同时演进。

**测试是事后验证**——代码写完了，测试确认它做了该做的事。补测试的过程本身就在产出价值：发现边界条件遗漏、行为不符预期的地方。但测试只能验证「已知场景」，它不能替代 code review 发现「没想到的场景」。

**编译期校验是事前拦截**——不允许不合法的配置进入系统。从 `data_parallel` 约束到参数-元数据一致性，每一条编译期规则都在回答：「哪些错误可以在最早的阶段被发现？」Pineapple 的两层守门（Apple 编译 + Go 加载）策略在这一轮更加完善——任何能在 Python 编译时拦截的错误，都不应该等到 Go 加载时才报。

**Code review 是全局视角**——P2 和 P3 是功能测试很难覆盖到的跨层语义 bug。类型断言静默失败、codegen 链路上的语义偏移，这些需要有人从端到端的视角审视整条数据流才能发现。review 的价值不在于挑出每一行的问题，而在于从「这段代码在真实场景下会怎样运行」的角度发现测试和编译器都拦不住的盲区。

三种机制互补而非替代。测试保证已知路径正确，编译期校验封杀不合法输入，review 发现测试和校验的盲区。项目的健壮性不取决于任何单一环节有多强，而取决于这三层网能不能把不同类型的问题各自拦住。
