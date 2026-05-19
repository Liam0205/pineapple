# 跨层语义校验

本指南描述当功能跨越 Python DSL、JSON 契约、Go 初始化与运行时时，如何避免“单层都看起来正常，但端到端结果悄悄错误”的缺陷。

## 适用范围

当任务涉及以下情况时使用本指南：

- 参数会穿过 Python -> JSON -> Go 边界
- 修改算子 Schema、codegen 或可选参数序列化逻辑
- 新增依赖 metadata 字段名的业务参数
- 新增算子或修复算子时需要确认非 happy path 语义

## 1. 枚举 JSON 边界类型空间

只要值会跨 Python -> JSON -> Go 传递，就不要只按“理想类型”验证。必须显式检查 JSON 的 6 种基础形态：

- `string`
- `float64`
- `bool`
- `nil`
- `[]any`
- `map[string]any`

实践要求：

- 在设计和测试时，把“缺失、null、字符串、数字、布尔、数组、对象”都过一遍
- Go 端做类型断言时保留 `ok` 布尔值，禁止用 `value, _ := x.(Type)` 静默丢弃失败信息
- 非预期类型要么显式报错，要么按文档化规则处理；不要默默回退到默认值或 miss 语义

反模式：

- 假设 Python 的 `int` 到 Go 里仍然是 `int`
- 在 Go 中写 `value, _ := x.(string)`，然后把空字符串当作正常路径继续执行

## 2. 校验 codegen 的语义，而不只是语法

codegen 验证不能停留在“生成代码能 import、能实例化”。还要验证生成代码在真实调用场景下产出的 JSON 语义是否正确，尤其是可选参数。

最少检查：

- 当调用方不传可选参数时，生成代码是否真的省略该 JSON 字段
- 当调用方显式传 `None` / `null` 时，是否与“不传”保持预期区分
- Go 反序列化后，运行时是否还能保留原本的 skip / default / missing 语义

执行方式：

1. 修改 Schema 或 codegen 后重生成产物
2. 构造至少一个“不要传可选参数”的调用样例
3. 追踪该样例的 compile 输出 JSON
4. 确认 Go 初始化后的行为与设计语义一致，而不是只确认代码能运行

## 3. 为每个新算子补至少一个边界值 E2E

单层测试无法发现跨层语义漂移。新增算子或修复跨语言参数时，必须补至少一个从 Python DSL 到最终结果的端到端用例。

每个新算子至少覆盖一个非 happy path 场景，优先从以下边界值中选择：

- 数字 ID，例如业务上是 key，但经 JSON 反序列化后变成 `float64`
- `nil` / `null` 值
- 未传可选参数
- 业务参数引用 metadata 字段名但输入 metadata 不满足约束

E2E 检查不是只看“有没有报错”，而要完整追踪：

- Python 调用时传了什么
- 编译出的 JSON 是什么
- Go 初始化时读到了什么类型/值
- 最终结果、skip 语义或错误路径是否符合预期

## 4. 识别并注册隐含契约

当业务参数的值本身就是 metadata 字段名时，这不是普通字符串参数，而是一个隐含契约。

典型例子：

- `lookup_key="item_id"` 隐含要求 `item_input` 包含 `item_id`
- `output_field="score"` 隐含要求 `item_output` 或相关 metadata 规则允许写入 `score`

处理要求：

- 不要把这种约束留到运行时偶然失败后再发现
- 在 Apple 编译期把它注册进 `_PARAM_METADATA_RULES`
- 为该规则补 compile-time validation，确保不一致配置被 `ValidationError` 拒绝

判断原则：如果某个业务参数的值会被当作字段名、列名、metadata key 或读写声明的一部分使用，就要先问自己：这里是否形成了 param -> metadata contract。

## 5. 缺陷排查顺序

遇到“结果不对但没有 crash”的问题时，优先按以下顺序排查：

1. Python 调用是否真的区分了“未传”和“传 `None`”
2. compile 后 JSON 是否出现了不该存在的字段或值类型变化
3. Go Init 是否对类型断言失败做了静默吞掉
4. 运行时是否把类型错误误当作普通 miss / default 路径
5. 该参数是否其实隐含 metadata 契约，但编译器未建模

## 最小检查清单

- 是否枚举了 JSON 边界的 6 种基础类型
- 是否避免了 Go 中静默丢弃类型断言失败
- 是否验证了“未传可选参数”的 codegen 语义
- 是否至少有一个非 happy path E2E 用例贯穿 Python -> JSON -> Go -> result
- 是否识别并注册了 param -> metadata 的隐含契约

## 6. 扩展点对等验证

验证各引擎对外暴露的扩展能力是否一致，而非仅验证已有功能输出。此维度覆盖"能力等价"——下游能否在三引擎间使用相同的集成模式。

检查点：

- middleware 是否能看到所有 HTTP 请求（包括未注册路径）
- 下游注入自定义 handler/endpoint 的 API 是否存在且语义一致
- Option pattern 覆盖面一致（相同的 withXxx 选项在三引擎均可用）
- 生命周期钩子对等（shutdown callback、reload 回调等）

方法：

- 编写"下游典型使用模式"用例，在三引擎各自实现并验证可行性
- 对未注册路径发送请求，验证 middleware 是否能拦截
- 对比三引擎的公共 API surface（不只是内部实现）
- 枚举 Server 级扩展点（add handler、wrap middleware、custom route），验证三引擎均暴露等价能力
