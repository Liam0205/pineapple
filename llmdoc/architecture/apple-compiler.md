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
- 输出匹配 `pine-go/internal/config/types.go` 的 JSON

编译器的输出是 Python 和 Go 之间的持久边界。

## 声明 API

### Flow 和 SubFlow

`apple/flow.py` 定义两个主要的面向用户的构建器：

- `Flow` — 带输入/输出契约、资源和可选 `storage_mode` / `log_prefix` / `debug` 的顶层声明
- `SubFlow` — 无独立契约的可复用算子片段，可任意层级嵌套

`SubFlow.__init__` 接受可选的契约声明参数：`common_input`、`common_output`、`item_input`、`item_output` 和 `required_resources`。这些声明不参与运行时（SubFlow 不是独立的执行单元），而是在 `compile()` 阶段供校验器使用：

- `required_resources`：`compile()` 校验 SubFlow 内引用的 `resource_name` 必须在此列表中声明
- `common_input` / `common_output` / `item_input` / `item_output`：为 SubFlow 声明输入输出契约，供未来校验扩展使用

两者继承 `_FlowBase`。`_FlowBase` 现在同时维护：

- `_ops` — 当前节点直挂的算子
- `_sub_flows` — 当前节点直挂的子 `SubFlow`
- `_child_order` — `("op", idx)` / `("sf", idx)` 的声明顺序账本，用于保留“算子与子流程穿插出现”的原始结构

这意味着 `SubFlow` 不再只是顶层 `Flow` 的平面附件；它本身也可以继续容纳嵌套 `SubFlow`，编译器后续按树结构递归处理。`Flow(sub_flows=[...])` 仍可用于初始化顶层子流程，但稳定 API 现在还包括 `_FlowBase.add_subflow(sf)`：

- 返回 `self`，可链式调用
- 保留与算子声明一致的插入顺序
- 拒绝名称中含 `/` 的 `SubFlow`，因为 `/` 已成为 JSON `pipeline_map` 的层级路径分隔符
- 可在未闭合的 `if_` / `elseif_` / `else_` 分支内调用；编译器会把当前活跃控制字段继承到该 `SubFlow` 子树中的每个算子

由于 `_FlowBase.__getattr__` 会把未知属性调用当作算子声明，误写成不存在的 `add_sub_flow(...)` 仍会被动态分发误解为声明了名为 `add_sub_flow` 的算子；稳定可用的方法名是 `add_subflow(...)`。

### 两条分发路径

Apple 支持两种声明算子的方式。

#### 动态分发

`_FlowBase.__getattr__` 将未知属性访问转为算子记录，因此 `flow.transform_copy(...)` 甚至 `flow.some_future_op(...)` 都会被接受。

特点：

- 无静态类型要求
- 算子名直接取自调用的属性名
- 元数据 kwargs 和业务参数在运行时分离
- `_add_op` 会提取引擎保留字段；`recall` 虽可传入但会被忽略并改由 `type_name` 前缀推导，`data_parallel` 和 `strict_common` / `strict_item` 则会被当作引擎级元数据保留在 `OpCall` 上，而不会混入业务 `params`

这是基线 API，也解释了为何 wheel 无需 `apple_generated/` 即可运行。

#### 类型化分发

`apple_generated/operators.py` 包含从 `apple.base.BaseOp` 继承的生成 helper 类。

特点：

- 从 Go 算子 Schema 生成
- 带类型的 `__call__` 签名，包含参数和元数据 kwargs
- 最终调用 `BaseOp._apply()` 追加 `OpCall`
- 与动态分发一致，在控制分支内声明时会继承当前活跃 branch 的 `skip` 字段列表，并把对应控制字段加入 `common_input`

这些是开发时的类型化编写便利，不是独立的执行路径。

### `OpCall` 作为编译器 IR

`apple/base.py` 定义 `OpCall`，这是两种分发方式记录的中间表示。它存储：

- `type_name`
- 业务参数
- 元数据字段（`common_input`、`common_output`、`item_input`、`item_output`）
- 默认值（`common_defaults`、`item_defaults`）
- 字段模式覆盖（`strict_common`、`strict_item`）
- 控制流字段如 `skip` 和 `for_branch_control`
- merge 祖先（`sources`）
- 引擎级 flags（`row_dependency`、`recall`、`data_parallel: int = 0`）
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

其中 `if_()` / `elseif_()` 的 `condition` 现在采用显式模板语法：字段引用必须写成 `{{field_name}}`，例如 `if_("{{item_count}} > 0")`。未包在 `{{...}}` 中的标识符不会再被当作字段依赖提取。

这是一项 Apple 编译期破坏性变更，目的是把“字段引用”与 Lua 表达式中的字符串字面量、关键字或其他标识符彻底区分开。旧写法如 `if_("item_count > 0")` 不再满足字段提取约定；而 `experiment_group_value == "treatment"` 这类表达式现在必须写成 `{{experiment_group_value}} == "treatment"`，否则编译器不会把 `experiment_group_value` 视为输入字段，同时也不会再误把字符串字面量 `"treatment"` 当作字段名。
DSL 层在用户调用 `end_if_()` 时还会立即做空分支校验：每个 branch 都必须至少有一个业务算子引用该 branch 的 `ctrl_field`。若某个分支只有控制算子、没有任何业务算子挂在该分支下，`apple/flow.py` 会直接抛出 `ValueError`。

### 控制流调用顺序校验

`apple/flow.py` 在 DSL 声明期还对控制流 API 的调用顺序做 fail-fast 校验：

- `elseif_()` 在 `else_()` 之后调用会立即抛出 `ValueError`——逻辑上 else 是默认分支，后面不能再追加条件分支
- `else_()` 重复调用会立即抛出 `ValueError`——同一个控制块只能有一个 else 分支

这些校验发生在声明期（用户构建 Flow 对象时），而非编译期，因此错误定位最为直接。

### 降级策略

`apple/control.py` 通过 `make_control_op` 将每个分支转为一个 `transform_by_lua` 算子。控制算子使用显式命名 `{kind}_{ctrl_index}`（如 `if_1`、`elseif_2`、`else_3`），使其在 DAG 可视化中可直观辨识为条件分支，而非淹没在 `transform_by_lua_XXXXXX` 的自动命名中。`type_name` 仍为 `transform_by_lua`，Go 引擎调度不受影响。

每个分支写入一个编译器生成的 common 字段，如：

- `_if_1`
- `_elif_1`
- `_else_1`

块内声明的分支算子接收：

- `skip=[<当前活跃控制字段>...]`
- 添加到 `common_input` 的对这些字段的依赖

嵌套控制流中，内层控制算子自身也继承外层控制字段；内层业务算子的 `skip` 同时包含外层与内层控制字段。分支内嵌套 `SubFlow` 时，父级控制字段会在递归遍历时传播到整个子树。若 `SubFlow` 内部也定义控制流，编译器会用 SubFlow 路径前缀重命名内部控制字段，例如 `_ranking_if_1`，避免与外层或兄弟 SubFlow 冲突。

`skip` 是控制流降级机制中的横切字段——它被四类路径共同读写：直接 attach（`_add_op` / `BaseOp._apply`）、SubFlow 继承（`_inject_inherited_skips(...)`）、字段重命名（`_rename_control_fields(...)` 产出的映射）、以及最终 JSON 发射。任何对 `skip` 类型或语义的变更都必须检查这四条路径的一致性。

运行时含义：

- 控制算子返回 `false` 时分支应执行
- 控制算子返回任意 Lua truthy 值时下游分支算子应跳过；只有 `nil` 和 `false` 视为 falsy

因此调度器的 skip 约定是“skip 列表中任一字段只要为 truthy 即跳过”，单个分支算子的本地语义仍可视为 `truthy = 跳过`、`false/nil = 运行`。这与 `apple/control.py` 发射的 Lua prior-check 保持一致：`elseif_` 与 `else_` 不再使用严格 `(f == true)`，而是直接使用 `(f)`，从而允许上游控制字段在手写 JSON 或其他边界输入中以 `1` 等非 bool truthy 值参与分支互斥。

### 嵌套控制流与 `inherited_skips`

当编译器递归遍历 Flow/SubFlow 结构树时，`apple/compiler.py` 会沿 traversal 显式传递一份 `inherited_skips` 列表，用来承接当前节点所处的所有外层控制分支守卫。

稳定语义是：

- 顶层普通算子若不在任何控制分支内，则 `skip` 为空列表或不输出该字段
- 位于单层 `if_` / `elseif_` / `else_` 分支内的算子，其 `skip` 列表包含当前分支自己的控制字段
- 位于控制分支内的嵌套 `SubFlow` 中的算子，会继承外层分支的 `skip` 列表
- 若嵌套 `SubFlow` 自身还声明了新的控制流，则内部业务算子的 `skip` 会是“外层 inherited skips + 当前内层控制字段”的拼接结果

因此 `add_subflow()` 现在可以安全地放在控制分支里：编译器不再把 branch guard 停留在当前层，而是把它继续传播到子树中的所有叶子算子。

该机制的目标不是改变 Go 侧调度协议，而是保证递归结构下控制语义与“若把所有算子手工写平”保持一致。

### SubFlow 内部控制字段的路径前缀化

控制字段名必须在整条 flow 的 common 域中全局唯一。为避免“外层控制块”和“SubFlow 内部控制块”生成同名 `_if_1` / `_else_1` 字段，编译器会对非顶层 `SubFlow` 内部生成的控制字段做路径前缀化。

示例：

- 顶层控制字段仍可能是 `_if_1`
- `inner` 这个 SubFlow 内部的控制字段会变成 `_inner_if_1`
- 更深层嵌套路径会继续把路径信息编码进字段名前缀，确保不同子树中的控制字段不会碰撞

这项前缀化只影响编译器生成的内部 common 字段名，不改变控制算子的公开命名规则：控制算子本身仍使用 `if_1`、`elseif_2`、`else_3` 这类显式 `OpCall.name`，而真正写入 frame 并供 skip 依赖引用的是带路径前缀的内部字段。

### `_rename_field` 对 SubFlow 内 Lua 脚本的处理

当控制字段被路径前缀化时，SubFlow 内的控制算子 Lua 脚本中的变量引用也需要同步更新。由于命名空间化后的字段名可能包含 `/` 等非法 Lua 标识符字符，`_rename_field` 使用 `_G["namespaced_field"]` 语法替换 Lua 脚本中的变量引用，而不是直接使用点号访问。例如，将 `_if_1` 替换为 `_G["_inner_if_1"]`。这确保重命名后的控制字段在 Lua 运行时可被正确访问。
注意 `_parent_skips` 在 `add_subflow()` 声明期捕获的是原始字段名；compile traversal 时会先经 `_rename_control_fields(...)` 生成当前节点的字段映射，再由 skip 注入阶段把这些引用统一到重命名后的字段名，确保继承链路与最终 JSON 产物一致。
### 条件字段提取

这一约束把字段依赖声明变成显式语法，而不再依赖对整段条件字符串做正则启发式扫描。稳定语义是：

- `{{field}}` 表示“这是要参与 `common_input` 推导的字段引用”
- 双花括号之外的内容按原样视为 Lua 表达式的一部分
- 字符串字面量、Lua 运算符、关键字，或其他未被 `{{...}}` 包裹的标识符，都不会参与字段提取

因此，像 `{{experiment_group_value}} == "treatment"` 这样的条件会只提取 `experiment_group_value`，不再把字符串常量 `"treatment"` 误识别为字段名。
## 编译流水线

`apple/compiler.py` 执行固定序列。新版流水线围绕 Flow/SubFlow 树执行“递归展平 + 局部多 pass 编译”，把原先 `_traverse()` 中靠注释维持的隐式顺序约束拆成独立步骤。后续校验仍依赖由这套流程产出的全局声明顺序。

### 步骤 1：递归遍历结构树，并在每个节点内执行局部多 pass

编译器从顶层 `Flow` 出发，递归遍历每个节点的 `_child_order`，但每个 Flow/SubFlow 节点在把本层内容写入全局结果前，会先对本层局部 `OpCall` 列表执行固定顺序的多 pass 处理。

局部 pass 的稳定顺序是：

1. `_rename_control_fields(local_ops, child_order, path)`
2. flatten + recurse
3. `_inject_inherited_skips(local_ops, child_order, inherited_skips)`
4. `_collect_exclusion_groups(node, field_renames, exclusion_groups)`

顺序不可交换：

- `rename` 必须先于 `inject`，因为父级继承下来的 skip 需要先映射到当前 SubFlow 最终使用的控制字段名
- `flatten + recurse` 负责保留全局算子顺序，后续校验和 Go 侧 DAG 都依赖这一顺序
- `collect exclusion groups` 必须在 rename 之后运行，否则记录到的互斥控制字段集合会与最终 JSON / skip 引用使用的字段名不一致

这套拆分替代了旧版 `_traverse()` 中的单体式原地处理逻辑，使三个控制流相关阶段都可以独立测试，同时由函数调用顺序而非注释来强制执行依赖关系。

#### 1. `_rename_control_fields(local_ops, child_order, path)`

该 pass 只处理当前节点“本地声明”的控制字段重命名，不负责递归子树。

稳定语义：

- 仅当 `path` 非空时生效，也就是仅对非顶层 `SubFlow` 做路径前缀化
- 把本层控制块生成的内部字段名（如 `_if_1`、`_else_1`）改写为带 SubFlow 路径前缀的名字
- 返回 `dict[str, str]` 形式的字段重命名表，供后续 skip 注入和互斥组收集复用

因此控制字段的“路径去冲突”现在是显式的独立 pass，而不是遍历时顺手改写的副作用。

#### 2. flatten + recurse

在完成本层字段重命名后，编译器继续按 `_child_order` 保留原始声明结构：

- 遇到 `("op", idx)` 时，把对应 `OpCall` 追加到全局 `all_ops`
- 遇到 `("sf", idx)` 时，生成层级路径 `parent/child`，继续递归遍历该 `SubFlow`
- 同时为每个 Flow/SubFlow 节点记录 `structures[path] = [("op", global_idx) | ("sf", subflow_path)]`

这一步同时确定两类稳定结果：

- 全局算子顺序：供后续命名、校验和 Go 侧 DAG 基础序列使用
- 层级结构账本：供后续发射 `pipeline_group.main.pipeline` 与 `pipeline_map`

递归遍历时，编译器还会把当前层级路径注入每个 `OpCall.subflow_path`：

- 顶层算子的 `subflow_path` 保持为空字符串
- 嵌套算子使用 `/` 连接的稳定路径，例如 `recall/candidates`
- 该字段只作为编译期诊断上下文，不会写入最终 JSON `operators` 配置

因此后续 validator 可以在不改变运行时契约的前提下，把声明错误精确归因到某个嵌套 `SubFlow`。

编译器会对遍历做两类结构保护：

- 复用/环检测：同一个 `SubFlow` 对象若被重复引用，视为 `SubFlow cycle or reuse detected`
- 路径唯一性：若生成的层级路径重复，则报 `duplicate SubFlow path`

因此稳定模型是“SubFlow 树”，而不是“可共享 DAG 片段”。

#### 3. `_inject_inherited_skips(local_ops, child_order, inherited_skips)`

该 pass 负责把当前节点外层分支守卫传播到本层子树中的子算子声明。

稳定语义：

- 把外层 `inherited_skips` 追加到子算子的 `skip`
- 同时把这些 skip 字段补入对应算子的 `common_input`
- 必须在 `rename` 之后执行，确保追加的是最终字段名，而不是重命名前的原始控制字段

这使“控制分支里再挂一个 SubFlow”与“手工把该 SubFlow 内算子写平到当前分支里”的控制语义保持一致。

#### 4. `_collect_exclusion_groups(node, field_renames, exclusion_groups)`

该 pass 从已闭合的控制块中收集互斥控制字段集合，用于表达同一组 if/elseif/else 分支之间的互斥关系。

稳定语义：

- 只收集已经闭合的控制块，不从未完成结构推导互斥信息
- 写入的字段名必须使用 rename 之后的最终名字
- 因此必须在 `_rename_control_fields(...)` 之后执行

把互斥组收集拆成独立 pass 后，控制字段命名与互斥关系不再耦合在同一段遍历副作用中。

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
- 自动名使用 `{type_name}_{MD5[:6].upper()}`，其中 hash 输入为显式语义元组（排除 `code_info`），确保在不同源文件位置声明的相同语义算子获得相同名称
- 自动名冲突时追加 `_N`

这创建了后续所有阶段使用的有序命名序列。

### 步骤 3：运行七项校验

校验采用 fail-fast，按特定顺序运行。

1. `validate_no_underscore_output`
2. `validate_field_coverage`
3. `validate_write_without_read`
4. `validate_data_parallel`
5. `validate_sources_references`
6. `validate_param_metadata_consistency`
7. `detect_dead_code`

顺序重要，因为每个后续规则假设算子序列和字段集已足够合理。`validate_data_parallel` 刻意放在写后未读之后、sources 校验之前：前者先确认字段声明本身合理，而 sources 校验依赖稳定的命名算子序列。`validate_sources_references` 又必须先于参数-元数据一致性与死代码检测运行，这样能更早拦截两类因果顺序错误：引用不存在的 source，以及引用虽存在但尚未在声明序列中出现的前向引用。

### 步骤 4：构建 operators dict

编译器为每个命名算子输出一个 JSON 对象，包含：

- `type_name`
- `$metadata`
- 可选 `$code_info`
- `recall`
- `sources`
- `skip`（字符串列表；Go 加载器兼容旧版单字符串）
- `for_branch_control`
- `row_dependency`
- `data_parallel`（仅当 `> 1` 时输出）
- `item_defaults`
- `common_defaults`
- `strict_common`（仅当非空时输出）
- `strict_item`（仅当非空时输出）
- `debug`
- 业务参数

这是 Go 配置加载器后续解析为 `pine-go/internal/config.OperatorConfig` 的对象。

其中 `sources` 相关的 `_resolve_source` 语义是直接透传：编译器直接返回 `source_type_hint`，不再做名字解析。原因是 source refs 在 DSL 层已经是最终算子名；用户通过 `sources=[...]` 传入的应是显式 `name=` 指定的算子名，编译器只负责原样写入 JSON。

### 步骤 5：构建 `pipeline_map`

每个非顶层 `SubFlow` 路径成为一个命名 pipeline，key 使用 `/` 连接层级，例如 `recall/candidates`。

每个 `pipeline` 列表中的条目保持声明顺序，可混合两类引用：

- 叶子算子名（引用 `operators` 中的条目）
- 子 `SubFlow` 路径（引用 `pipeline_map` 中的下一层 pipeline）

因此 `pipeline_map` 现在表达的是可递归展开的树，而不是仅容纳一层叶子算子的平面分组表。

### 步骤 6：构建 `pipeline_group`

Apple 仍输出单个名为 `main` 的 group，但 `pipeline_group.main.pipeline` 现在直接保留顶层声明序列：

- 顶层叶子算子直接写入算子名
- 顶层子 `SubFlow` 写入其路径引用

编译器不再生成 `_main_<flow>` 这类合成 SubFlow 来包装顶层算子。顶层结构直接体现在 `pipeline_group.main.pipeline` 中。

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
- `storage_mode`（当 `Flow` 构造时指定了 `storage_mode` 参数）
- `log_prefix`（当 `Flow` 构造时指定了 `log_prefix` 参数）
- `debug`（当 `Flow` 构造时指定了 `debug=True/False` 参数）
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

### 校验报错定位格式

`apple/validator.py` 通过 `_op_location(name, op)` 统一构造逐算子校验错误前缀。所有 per-operator `ValidationError` 都应复用这一路径，而不是各自拼接字符串。

稳定格式分三层：

- 基础头部始终是 `operator 'name'`
- 若 `OpCall.subflow_path` 非空，则在头部追加 ` [path/to/subflow]`
- 若 `OpCall.code_info` 可用，则继续追加换行的 `defined at: file:line ...`

因此同一类字段/参数错误，在不同嵌套层级下会呈现为：

- 顶层算子：`operator 'transform_xxx': ...`
- 子流程算子：`operator 'transform_xxx' [recall/candidates]: ...`
- 若带源码位置，还会在消息中出现 `defined at: path/to/file.py:123 ...`

该定位信息只服务于 Apple compile-time 诊断，不参与 JSON 发射，也不会影响 Go 侧配置加载契约。它的目标是让深层 `SubFlow` 中的字段覆盖、写后未读、`data_parallel` 和参数-元数据一致性错误，都能直接指向“哪条子流程路径、哪一行声明代码”触发了问题。

### 4. 死代码检测

`detect_dead_code` 标记产出的输出未被任何下游消费者读取且 flow 输出契约也未暴露的算子。

豁免：

- recall 算子
- 控制算子
- 无输出的 observe 类算子

编译器在发现死算子时抛出 `ValidationError`。

### 5. 数据并行约束

`validate_data_parallel` 在 Apple 编译期对 `data_parallel` 做 fail-fast 结构性校验。

当 `data_parallel > 1` 时：

- `type_name` 必须以 `transform_` 开头，也就是仅允许 Transform 类算子启用数据并行
- `common_output` 必须为空，避免多个并发 worker 竞争写入 common 域

Apple 侧**不再维护** unsafe transforms 名单。算子是否具备并发安全能力（`ConcurrentSafe` 接口）由 Go 引擎在 `NewEngine` → `validateDataParallel` 阶段通过接口断言检查。这消除了此前 Python/Go 双端名单的漂移风险，实现了单一事实源。

Apple 结构校验的目标是把明确的配置错误（非 Transform、有 common_output）前移到编译期，尽早暴露；能力校验则由持有算子实例的 Go 侧权威负责。

### 6. `sources` 引用顺序与存在性

`validate_sources_references` 按命名算子的声明顺序遍历，并维护一个 `seen` 集合作为“已经出现过的算子名”账本。

对每个算子的 `sources` 条目：

- 若 source 名既不在 `seen` 中，也不在全局算子名集合中，报 `does not exist`
- 若 source 名在全局集合中、但尚未进入 `seen`，报 `forward reference`
- 只有已经出现在当前算子之前的 source 才是合法引用

这条规则一次覆盖两类高风险配置错误：

- 忘记给上游算子写显式 `name=`，却在 `sources=[...]` 中引用一个不存在的名字
- 引用了声明顺序上位于未来的算子，造成 DAG 因果倒置

错误消息应复用 `_op_location(name, op)`，因此在嵌套 `SubFlow` 中同样会带上 `subflow_path` 与源码位置。

### 7. 参数-元数据一致性

`validate_param_metadata_consistency` 使用规则表 `_PARAM_METADATA_RULES` 校验业务参数与元数据声明的一致性。

当前规则：

- `transform_resource_lookup` 的 `lookup_key` 必须出现在 `item_input` 中
- `transform_resource_lookup` 的 `output_field` 必须出现在 `item_output` 中

规则表是可扩展的——未来如果有其他算子也存在「业务参数隐含元数据要求」的情况，只需往 `_PARAM_METADATA_RULES` 中添加条目。

此规则防止业务参数与元数据声明不匹配导致运行时静默错误：DAG builder 和 `BuildInput` 只追踪 `$metadata`，如果 `lookup_key` 不在 `item_input` 中，运行时不会为该算子构造对应字段，lookup 变成 silent no-op。

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

Apple 支持两层 debug 声明：

- `OpCall.debug` / 逐算子 `debug=True`：在单个算子 JSON 对象上输出 `debug`
- `Flow(debug=...)`：在步骤 9 作为根级 `debug` 字段写入 JSON

两者职责不同：

- 逐算子 `debug` 是细粒度配置，面向单个算子
- 根级 `debug` 是 flow 级默认开关，沿 `Flow(...)` → 根级 JSON → `pine-go/internal/config.RootConfig` → `pine.NewEngine()` 传递，并在 Go 侧展开为”所有算子都开启 debug”

这使 `debug` 成为继 `storage_mode`、`log_prefix` 之后第三个遵循同一路径下沉的 root-level 配置字段。

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

1. `pine-go/operators/` 下的 Go 算子 Schema 注册
2. `pine-go/pkg/codegen/` 中的 Go codegen
3. `apple_generated/` 中生成的 Python helper 类
4. Apple 编译输出 JSON
5. Go 运行时消费 JSON

编译器因此位于生成的声明 helper 和运行时配置消费之间。

## 需要保持的重要不变量

1. **Apple 输出 JSON，而非可执行运行时对象。** 保持基于文件/Schema 的边界。
2. **校验使用递归遍历产出的全局声明顺序。** 该顺序必须与运行时递归展开后的顺序保持对齐。
3. **控制条件中的字段引用必须显式模板化。** `apple/control.py` 只会为 `{{field_name}}` 形式生成 `common_input` 依赖；Lua 发射前再移除模板标记。
4. **下划线前缀字段保留。** 用户输出不应与编译器/运行时内部冲突。
5. **资源引用在算子序列构建后校验。** 无声明的 `resource_name` 参数是编译错误。
6. **动态分发在无生成 helper 时仍可用。** `apple_generated/` 是便利，不是语言核心。
7. **`data_parallel` 约束采用双层校验。** Apple compile time 的 `validate_data_parallel` 与 Go 引擎加载期的 `validateDataParallel` 必须保持一致，以便同时提供 fail-fast 体验和运行时边界保护。
8. **`sources` 引用也必须遵守声明顺序。** Apple compile time 的 `validate_sources_references` 只允许引用已经出现在当前算子之前的命名上游；不存在的名字报 `does not exist`，存在但尚未出现的名字报 `forward reference`。这保证显式 merge/source 边不会把 DAG 拉成“未来节点依赖过去节点”的因果倒置。
9. **控制分支守卫会沿 SubFlow 递归传播，且 skip 采用 Lua truthiness。** `apple/compiler.py` 通过 `inherited_skips` 把外层分支控制字段继续传给嵌套 `SubFlow` 中的算子，因此“控制分支里再 add_subflow()”与手工写平后的控制语义保持一致；`apple/control.py` 发射的 prior-check 与 Go 调度器的 skip 判定都以 Lua truthiness 为准，只有 `nil` 和 `false` 不触发跳过。
10. **SubFlow 内部控制字段必须做路径去冲突。** 非顶层 `SubFlow` 中生成的 `_if_*` / `_else_*` 字段需要带路径前缀，避免与外层或兄弟子树的控制字段共用 common 域名称。
11. **根级配置字段沿固定扩展路径下沉。** 顶层 `Flow(...)` 参数经 `apple/compiler.py` 步骤 9 条件写入根级 JSON，再由 `pine-go/internal/config/types.go` 的 `RootConfig` 消费；`storage_mode`、`log_prefix` 与 `debug` 都遵循这一模式。
12. **编译器 traversal 幂等。** `_traverse()` 的局部多 pass 不可修改原始 Flow/SubFlow 上的 `OpCall` 对象。当前通过复制本地 ops 并把重命名、skip 注入、互斥组收集拆成独立 pass 来保证 `compile_dict()` 可重复调用；如果后续新增 traversal 逻辑需要改写 IR，必须同样保持幂等。
    - 测试覆盖要求应显式包含“同一 Flow 连续编译两次输出完全一致”的场景，并继续覆盖“控制分支内嵌 SubFlow”的路径：至少验证包含 branch 内 `SubFlow` 的 `Flow` 连续多次 `compile_dict()` / `compile_to_json()` 结果一致，避免 skip 继承、控制字段重命名或其他 traversal 侧效应在该路径上累积。
    - 当前测试拆分为四类：`TestCompileIdempotency` 覆盖重复编译稳定性；`TestRenameControlFields` 覆盖控制字段重命名 pass；`TestInjectInheritedSkips` 覆盖外层 skip 传播 pass；`TestCollectExclusionGroups` 覆盖闭合控制块的互斥组收集。

## 检索指针

- Flow API 和控制栈行为：`apple/flow.py`
- 编译器编排：`apple/compiler.py`
- 校验逻辑：`apple/validator.py`
- 控制流降级 helper：`apple/control.py`
- 编译器 IR 和类型化 helper 基类：`apple/base.py`
- 资源声明类型：`apple/resource.py`
- 生成的类型化算子：`apple_generated/operators.py`
