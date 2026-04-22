# Apple 编译器架构

本文档说明 Apple DSL 如何记录流水线并将其编译为 Go 引擎消费的 JSON 契约。

## 适用范围

当任务涉及以下文件时使用本文档：

- `apple/flow.py`
- `apple/compiler.py`
- `apple/validator.py`
- `apple/control.py`
- `apple/resource.py`
- `apple/base.py`
- `apple_generated/`

## 在系统中的角色

Apple 是 Pineapple 的声明侧。它不执行流水线。它的职责是：

- 提供流水线声明的 Python API
- 将算子调用记录为结构化的 `OpCall` 值
- 在运行时之前校验声明正确性
- 将控制流降级为普通算子 + skip 字段
- 输出匹配 `internal/config/types.go` 的 JSON

编译器的输出是 Python 和 Go 之间的持久边界。

## 声明 API

### Flow 和 SubFlow

`apple/flow.py` 定义两个主要的面向用户的构建器：

- `Flow` — 带输入/输出契约、资源和可选 `storage_mode` 的顶层声明
- `SubFlow` — 无独立契约的可复用算子片段

两者继承 `_FlowBase`，持有算子列表和控制流记账。

`SubFlow` 通过 `Flow(sub_flows=[...])` 在构造 `Flow` 时传入，不存在独立的 `add_sub_flow()` 方法。由于 `_FlowBase.__getattr__` 会把未知属性调用当作算子声明，误写成 `flow.add_sub_flow(...)` 会被动态分发误解为声明了名为 `add_sub_flow` 的算子，而不是追加子流程。

### 两条分发路径

Apple 支持两种声明算子的方式。

#### 动态分发

`_FlowBase.__getattr__` 将未知属性访问转为算子记录，因此 `flow.transform_copy(...)` 甚至 `flow.some_future_op(...)` 都会被接受。

特点：

- 无静态类型要求
- 算子名直接取自调用的属性名
- 元数据 kwargs 和业务参数在运行时分离

这是基线 API，也解释了为何 wheel 无需 `apple_generated/` 即可运行。

#### 类型化分发

`apple_generated/operators.py` 包含从 `apple.base.BaseOp` 继承的生成 helper 类。

特点：

- 从 Go 算子 Schema 生成
- 带类型的 `__call__` 签名，包含参数和元数据 kwargs
- 最终调用 `BaseOp._apply()` 追加 `OpCall`

这些是开发时的类型化编写便利，不是独立的执行路径。

### `OpCall` 作为编译器 IR

`apple/base.py` 定义 `OpCall`，这是两种分发方式记录的中间表示。它存储：

- `type_name`
- 业务参数
- 元数据字段（`common_input`、`common_output`、`item_input`、`item_output`）
- 默认值
- 控制流字段如 `skip` 和 `for_branch_control`
- merge 祖先（`sources`）
- `row_dependency`
- `debug`
- `code_info`
- 可选的显式 `name`

编译在有序 `OpCall` 值上操作。

## 控制流降级

Go 引擎没有原生 if/else 构造。Apple 在编译器中完全降级控制流。

### 用户 API

`_FlowBase` 提供：

- `if_(condition)`
- `elseif_(condition)`
- `else_()`
- `end_if_()`

这些操作内部控制块栈并发出控制算子。

DSL 层在用户调用 `end_if_()` 时还会立即做空分支校验：每个 branch 都必须至少有一个业务算子引用该 branch 的 `ctrl_field`。若某个分支只有控制算子、没有任何业务算子挂在该分支下，`apple/flow.py` 会直接抛出 `ValueError`。

### 降级策略

`apple/control.py` 通过 `make_control_op` 将每个分支转为一个 `transform_by_lua` 算子。

每个分支写入一个编译器生成的 common 字段，如：

- `_if_1`
- `_elif_1`
- `_else_1`

块内声明的分支算子接收：

- `skip=<该控制字段>`
- 添加到 `common_input` 的对同一字段的依赖

运行时含义：

- 控制算子返回 `false` 时分支应执行
- 控制算子返回 `true` 时下游分支算子应跳过

因此调度器的 skip 约定是 `true = 跳过`，`false = 运行`。

### 条件字段提取

对于控制算子，`extract_fields()` 启发式扫描 Lua 条件字符串中引用的字段名。这些字段被添加到控制算子的 `common_input` 集合，连同 `elseif` 和 `else` 逻辑所需的先前分支控制字段。

## 编译流水线

`apple/compiler.py` 执行固定序列。流水线很重要，因为后续步骤假设前面的步骤已稳定了排序和命名。

### 步骤 1：展平子流程

所有声明的 `SubFlow` 算子列表先拼接，然后是主流程自身的算子。

编译器还记录每个子流程的 `[start, end)` 切片，以便后续重建 `pipeline_map` 条目。

### 步骤 1b：校验控制块闭合

编译器在命名和字段校验之前检查 `flow._ctrl_stack` 与每个 sub_flow 的 `_ctrl_stack` 是否为空。

若任一栈非空，则抛出 `ValidationError`。

此校验必须先于命名和字段校验运行，因为未关闭的控制块会导致后续算子的 `skip` 字段不正确。

注意这里与 DSL 层的职责分工不同：控制块是否闭合是在编译入口统一检查；而 `end_if_()` 的空分支检测发生在 `apple/flow.py` 中，属于用户调用 Flow API 时立即触发的声明期校验。

### 步骤 2：生成唯一算子名

每个算子需要一个稳定的 JSON 键。

命名规则：

- 显式 `name=` 直接使用
- 显式名必须全局唯一
- 自动名使用 `{type_name}_{MD5[:6].upper()}`
- 自动名冲突时追加 `_N`

这创建了后续所有阶段使用的有序命名序列。

### 步骤 3：运行四项校验

校验采用 fail-fast，按特定顺序运行。

1. `validate_no_underscore_output`
2. `validate_field_coverage`
3. `validate_write_without_read`
4. `detect_dead_code`

顺序重要，因为每个后续规则假设算子序列和字段集已足够合理。

### 步骤 4：构建 operators dict

编译器为每个命名算子输出一个 JSON 对象，包含：

- `type_name`
- `$metadata`
- 可选 `$code_info`
- `recall`
- `sources`
- `skip`
- `for_branch_control`
- `row_dependency`
- `item_defaults`
- `common_defaults`
- `debug`
- 业务参数

这是 Go 配置加载器后续解析为 `internal/config.OperatorConfig` 的对象。

其中 `sources` 相关的 `_resolve_source` 语义是直接透传：编译器直接返回 `source_type_hint`，不再做名字解析。原因是 source refs 在 DSL 层已经是最终算子名；用户通过 `sources=[...]` 传入的应是显式 `name=` 指定的算子名，编译器只负责原样写入 JSON。

### 步骤 5：构建 `pipeline_map`

每个子流程成为一个命名 pipeline，包含分配给该片段的算子名。主流程自身的直接算子归入内部 `_main_*` pipeline 条目。

### 步骤 6：构建 `pipeline_group`

Apple 当前输出单个名为 `main` 的 group，其 pipeline 列表保持 pipeline-map 条目的展平顺序。

### 步骤 7：构建 `flow_contract`

顶层契约复制 `Flow` 声明的：

- `common_input`
- `item_input`
- `common_output`
- `item_output`

当 `Flow._common_output` 或 `Flow._item_output` 为 `None`（用户未传该参数）时，编译器仍会在 JSON 中写入 `[]`。该编码刻意表示"该维度不输出任何字段"；Go 引擎会为该侧返回空 map，因此 `None` 与 `[]` 在持久 JSON 契约上都表示"不要输出任何字段"。

此契约后续在引擎请求/结果边界强制执行。

### 步骤 8：校验资源引用

核心算子校验之后，`_validate_resource_refs` 扫描业务参数中的 `resource_name`，检查每个名称是否匹配已声明的 `flow.resource(...)` 条目。

### 步骤 9：组装根元数据

编译器添加：

- `_PINEAPPLE_VERSION`（来自 `apple/_version.py`）
- `_PINEAPPLE_CREATE_TIME`（UTC ISO 时间戳）
- 可选 `storage_mode`（当 `Flow` 构造时指定了 `storage_mode` 参数）
- 可选 `resource_config`

### 步骤 10：序列化为 JSON

`compile_to_json()` 是对结果 dict 的薄包装 `json.dumps(..., indent=2)`。

## 校验规则

编译器的校验逻辑是面向声明的而非面向运行时的。

### 1. 禁止下划线前缀的用户输出

`validate_no_underscore_output` 将 `_` 前缀输出保留给引擎/编译器内部。

适用于：

- flow 级声明输出
- 逐算子声明输出

豁免：

- 标记 `for_branch_control=True` 的编译器生成控制算子

### 2. 字段覆盖

`validate_field_coverage` 按顺序遍历命名算子序列。

状态：

- `available_common`，从 flow 的 common input 契约初始化
- `available_item`，从 flow 的 item input 契约初始化

对每个算子：

- 所有声明的输入必须已可用
- 然后该算子声明的输出加入可用集

内部 `_` 前缀输入字段被忽略，使编译器生成的控制字段不触发误报。

### 3. 写后未读

`validate_write_without_read` 检测覆写上游已产出的字段而未先读取的情况。

目的：

- 捕获声明顺序中可疑的意外覆写
- 强制作者通过输入元数据显式化依赖

控制流豁免：

- 设置了 `skip` 的算子被豁免
- 它们的输出也不计入全局"已写"集

这使互斥的 if/elseif/else 分支可以写入相同字段。

### 4. 死代码检测

`detect_dead_code` 标记产出的输出未被任何下游消费者读取且 flow 输出契约也未暴露的算子。

豁免：

- recall 算子
- 控制算子
- 无输出的 observe 类算子

编译器在发现死算子时抛出 `ValidationError`。

## 关键不变量：校验顺序必须与执行顺序对齐

校验正确性取决于编译器使用的算子顺序与运行时使用的顺序假设一致。

原因：

- 字段覆盖假设更早的声明是更晚消费者的生产者
- 写后未读假设更早的写入是因果在先的
- 死代码检测按声明序列推理下游消费

这仅成立因为运行时的 DAG 构建也使用展平声明顺序作为冒险追踪和平局打破的基础序列。若编译时顺序和运行时顺序分歧，校验可能批准执行行为与分析不同的流水线。

## 元数据和默认值语义

Apple 输出的元数据被 Go 引擎直接消费。

### `$metadata`

逐算子元数据包含：

- `common_input`
- `common_output`
- `item_input`
- `item_output`

这些字段不只是文档。它们驱动：

- 编译器校验
- 运行时输入投影
- DAG 冒险推导
- 生成的算子文档

### 默认值

Apple 可附加：

- `common_defaults`
- `item_defaults`

这些成为配置的一部分，在构建算子输入快照时由 Go DataFrame 应用。

### Debug

`debug=True` 按算子输出，后续告知运行时在 trace 中捕获输入/输出快照。

### 行依赖

`row_dependency=True` 是声明元数据，后续告知 Go DAG 构建器在 item 级依赖推导中读取 `_row_set_` 哨兵。

## 资源声明模型

`apple/resource.py` 定义资源的声明侧。

### 资源对象

- `BaseResource` 是生成的资源类基类。
- `ResourceDecl` 是序列化的声明形式。

`Flow` 通过 `flow.resource(name, instance)` 记录资源。

### 输出形状

资源在 `resource_config` 下输出，包含：

- 资源名
- 资源类型
- 刷新间隔
- 参数

Go 服务端资源管理器后续加载这些定义并将活值注入请求上下文。

## 与 Go 代码生成的关系

Apple 的类型化 helper 类从 Go 生成，而非反向。

权威流向：

1. `operators/` 下的 Go 算子 Schema 注册
2. `pkg/codegen/` 中的 Go codegen
3. `apple_generated/` 中生成的 Python helper 类
4. Apple 编译输出 JSON
5. Go 运行时消费 JSON

编译器因此位于生成的声明 helper 和运行时配置消费之间。

## 需要保持的重要不变量

1. **Apple 输出 JSON，而非可执行运行时对象。** 保持基于文件/Schema 的边界。
2. **校验使用展平声明顺序。** 该顺序必须与运行时排序假设保持对齐。
3. **控制流在运行时之前完全降级。** 引擎应继续将其视为普通算子 + skip 字段。
4. **下划线前缀字段保留。** 用户输出不应与编译器/运行时内部冲突。
5. **资源引用在算子序列构建后校验。** 无声明的 `resource_name` 参数是编译错误。
6. **动态分发在无生成 helper 时仍可用。** `apple_generated/` 是便利，不是语言核心。

## 检索指针

- Flow API 和控制栈行为：`apple/flow.py`
- 编译器编排：`apple/compiler.py`
- 校验逻辑：`apple/validator.py`
- 控制流降级 helper：`apple/control.py`
- 编译器 IR 和类型化 helper 基类：`apple/base.py`
- 资源声明类型：`apple/resource.py`
- 生成的类型化算子：`apple_generated/operators.py`
