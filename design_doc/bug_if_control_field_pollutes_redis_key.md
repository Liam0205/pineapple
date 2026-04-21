# Bug: if_ 控制字段污染 Redis key 构建

## 现象

在 `if_` 分支内使用 `transform_redis_get` 时，Redis key 中会混入控制字段的布尔值。

例：pipeline 中 `transform_redis_get` 配置了 `key_prefix="user:blocked_creators:"` 和 `common_input=["user_id"]`，
期望 key 为 `user:blocked_creators:12345`，实际 key 为 `user:blocked_creators:false:12345`。

## 根因分析

### 1. 编译器行为（有意设计）

`apple/flow.py` 的 `_add_op` 方法在 `if_` 分支内为算子注入控制字段：

```python
if self._ctrl_stack:
    block = self._ctrl_stack[-1]
    if block.branches:
        branch = block.branches[-1]
        call.skip = branch.ctrl_field
        if branch.ctrl_field not in call.common_input:
            call.common_input = [branch.ctrl_field] + call.common_input
```

这里将 `branch.ctrl_field`（如 `_if_3`）同时设为 `skip` 字段和追加到 `common_input`。

目的：pineapple 的 DAG 调度器通过字段读写关系推断执行依赖。将控制字段加入 `common_input` 是为了建立 RAW 依赖——确保 `transform_by_lua`（写 `_if_3`）先于分支内算子（读 `_if_3`）执行。这是正确的 DAG 语义。

### 2. 算子行为（缺陷）

`transform_redis_get` 用所有 `common_input` 字段值拼接 Redis key suffix：

```go
func buildKeySuffix(in *pine.OperatorInput, fields []string) string
```

它没有区分"业务输入字段"和"DAG 调度用的控制字段"，导致 `_if_3` 的值（`false`）被拼入 key。

`transform_redis_set` 同理——它用 `common_input` 的前 N-1 个字段构建 key，最后一个作为 value。如果控制字段被注入到 `common_input[0]`，key 和 value 的语义都会错位。

### 3. 影响范围

所有在 `if_` 分支内使用的、依赖 `common_input` 字段值做业务逻辑的算子都可能受影响。`transform_redis_get` 和 `transform_redis_set` 是最典型的案例，因为它们直接用字段值构建外部系统的 key。

纯粹读写 pineapple dataframe 的算子不受影响——控制字段的值只是被读取后忽略。

## 可能的修复方向

### 方向 A：引擎侧（推荐）

在 `transform_redis_get` / `transform_redis_set` 的 `buildKeySuffix` 中，排除 `skip` 字段。算子可以通过 `MetadataHolder` 访问自身的 `Skip` 配置，构建 key 时跳过该字段名。

优点：改动局部，不影响 DAG 语义。
缺点：每个依赖 `common_input` 值做业务逻辑的算子都需要自行处理。

### 方向 B：编译器侧

让编译器不将控制字段注入 `common_input`，改为用其他机制（如 `$depends_on` 或显式边声明）建立 DAG 依赖。

优点：从根源消除问题，所有算子自动受益。
缺点：需要引擎 DAG 构建器配合支持新的依赖声明方式，改动面大。

### 方向 C：混合

编译器仍注入控制字段到 `common_input`（保持 DAG 语义），但引擎的 `OperatorInput.GetCommon` 自动过滤以 `_` 开头的内部字段，使算子业务逻辑看不到控制字段。

优点：一次修改，所有算子受益；DAG 语义不变。
缺点：引入了"内部字段"的隐式命名约定。

## 修复

采用方向 C 变体实施。在两处过滤 skip 字段，DAG 推导不受影响：

1. `pine.go`：`SetMetadata` 调用前从 `commonInput` 中剔除 `skip` 字段，使算子实例的 `o.CommonInput` 不含控制字段。
2. `internal/runtime/scheduler.go`：`BuildInput` 调用前同样过滤，使 `OperatorInput` 不含控制字段值。

同步补充了测试，验证 skip 字段不参与 key 构建。
