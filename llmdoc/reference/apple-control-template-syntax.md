# Apple DSL 控制流模板语法

本参考文档说明 Apple DSL 中 `if_()` / `elseif_()` 条件表达式的稳定写法，以及它如何映射到编译期字段依赖提取。

## 适用范围

当任务涉及以下内容时使用本文档：

- `apple/control.py`
- `apple/flow.py`
- `apple/tests/test_compiler.py`
- `apple/tests/test_validator.py`
- `apple/tests/test_e2e.py`
- README 或 design doc 中的 Apple 控制流示例

## 核心规则

`if_()` 与 `elseif_()` 的条件字符串里，字段引用必须使用 `{{field_name}}` 模板语法显式标记。

示例：

- 新语法：`if_("{{item_count}} > 0")`
- 旧语法：`if_("item_count > 0")``

稳定语义是：

- `{{field_name}}` 表示该字段会被 Apple 编译器提取到控制算子的 `common_input`
- 双花括号之外的文本按原样保留为 Lua 条件表达式的一部分
- 未包在 `{{...}}` 中的标识符不会被当作字段依赖

`else_()` 没有用户条件，因此不涉及模板标记。

## 为什么需要模板语法

旧实现会对整段条件字符串做正则启发式扫描，尝试自动识别字段名。这种写法无法可靠区分：

- 字段引用
- Lua 关键字或运算符
- 字符串字面量中的单词

典型问题是：

- 表达式 `experiment_group_value == "treatment"` 中，旧逻辑可能把字符串字面量 `"treatment"` 误识别为字段名

模板语法把字段引用变成显式声明，从而消除歧义。

## 编译期行为

`apple/control.py` 中有两个稳定步骤：

1. `extract_fields()` 只提取 `{{...}}` 内部的标识符，用于生成控制算子的 `common_input`
2. `_strip_template()` 在生成 Lua 脚本前移除模板标记，因此最终发射到 `transform_by_lua` 的条件仍是普通 Lua 表达式

例如：

- DSL 条件：`{{experiment_group_value}} == "treatment"`
- 提取的字段依赖：`["experiment_group_value"]`
- 发射的 Lua 条件：`experiment_group_value == "treatment"`

Go 运行时不需要理解模板语法；它只消费 Apple 编译后的普通 JSON 与 Lua 脚本。

## 写法示例

### 比较数值字段

```python
flow.if_("{{item_count}} > 0") \
    .some_op(...) \
.end_if_()
```

### 比较字段与字符串字面量

```python
flow.if_("{{experiment_group_value}} == \"treatment\"") \
    .some_op(...) \
.end_if_()
```

### 复合条件

```python
flow.elseif_("{{fallback_enabled}} ~= nil and {{item_count}} > 10") \
    .fallback_op(...) \
```

编译器会提取 `fallback_enabled` 与 `item_count` 两个字段，并在发射 Lua 前去掉模板标记。

## 迁移要点

当更新旧 Apple DSL 条件时：

1. 只给真实字段引用加上 `{{...}}`
2. 不要给字符串字面量加模板标记
3. 不要给 Lua 关键字、运算符或常量加模板标记
4. 若条件中未对任何字段加模板标记，编译器就不会为该条件推导对应的字段依赖

常见迁移示例：

- `if_("item_count > 0")` → `if_("{{item_count}} > 0")`
- `elseif_("fallback_enabled ~= nil")` → `elseif_("{{fallback_enabled}} ~= nil")`
- `if_("experiment_group_value == \"treatment\"")` → `if_("{{experiment_group_value}} == \"treatment\"")`

## 兼容性边界

这是 Apple DSL 的编译期破坏性变更：

- Python 侧控制流条件必须迁移到模板语法
- 生成出的 Lua 脚本与 JSON 结构未新增 Go 侧协议字段
- Go 引擎、DAG 调度与运行时控制流机制保持不变

因此受影响的是 DSL 作者、示例文档和 Apple 测试，而不是 Go 运行时接口。

## 检索指针

- 控制流降级与编译流水线：`llmdoc/architecture/apple-compiler.md`
- 引擎侧 skip / 控制字段透明性：`llmdoc/architecture/dag-engine.md`
- 设计示例：`design_doc/06_json_config.md`
- 实现入口：`apple/control.py`
