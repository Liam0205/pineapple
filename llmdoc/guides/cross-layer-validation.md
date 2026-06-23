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

### Codegen markdown 输出 byte-equal gate（跨引擎）

单元测试 pin 单个 helper（`pythonLiteral` / `formatG` / `pascalCaseEnum`）**不能**替代跨引擎 byte-equal diff——多个独立实现路径可能各自合规但总体输出仍漂移。任何 codegen markdown / Python 产物（`apple_generated/` / `doc/operators/`）的改动必须在 cross-validate 中跑 Go-vs-Java 与 Go-vs-cpp 的字节对比：

- 入口在 `scripts/cross-validate/01-codegen-schema.sh` 的 1d / 1e 两节
- 1e 是 Go-vs-Java + Go-vs-cpp 双 arm，覆盖 `doc/operators/*.md` 与 `apple_generated/` 的 `diff -r`
- 任一 arm 缺失或被静默跳过都不能通过——`CPP_CODEGEN` 未设置时 1d / 1e 的 cpp arm 应明确报"skipped"而非"passed"

历史教训：pine-java 的 doc render 第一轮 review 修了 `pascalCaseEnum`、`toPythonLiteral` 单测仍漏报，第二轮才发现 sortedParams required-first 顺序 drift，第三轮发现 pine-cpp 根本没 markdown emit 路径。byte-equal gate 锁住后这类漂移会在第一轮 PR 内被即刻暴露。详见 `memory/reflections/redis-cascade-safety-and-observability.md`。

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

验证各引擎对外暴露的扩展能力是否一致，而非仅验证已有功能输出。此维度覆盖"能力等价"——下游能否在各运行时间使用相同的集成模式。

检查点：

- middleware 是否能看到所有 HTTP 请求（包括未注册路径）
- 下游注入自定义 handler/endpoint 的 API 是否存在且语义一致
- Option pattern 覆盖面一致（相同的 withXxx 选项在各运行时均可用）
- 生命周期钩子对等（shutdown callback、reload 回调等）

方法：

- 编写"下游典型使用模式"用例，在各运行时各自实现并验证可行性
- 对未注册路径发送请求，验证 middleware 是否能拦截
- 对比各运行时的公共 API surface（不只是内部实现）
- 枚举 Server 级扩展点（add handler、wrap middleware、custom route），验证各运行时均暴露等价能力

## 7. 声明→生效端到端路径校验

当 Apple DSL 引入或修改声明侧字段（如 `strict_common`、`debug`），且该字段最终需要被运行时消费时，必须验证完整的声明→编译→运行时链路，而非仅验证编译产出的 JSON 格式正确。

核心问题：Apple DSL 和运行时通过 JSON 解耦，一侧的键名变更不会导致另一侧编译失败——JSON 反序列化对未知键静默忽略。这意味着"Apple 产出的 JSON 在运行时是否被实际读取"需要显式验证。

检查清单：

1. **Apple DSL 侧**：`apple/base.py` 的 `OpCall` 字段名 → `apple/compiler.py` emit 的 JSON 键名 → `apple/flow.py` 的 `_add_op` meta_keys 集合，三处必须一致
2. **运行时侧**：`pine-go/internal/config/types.go` 的 `OperatorConfig` JSON tag → `pine-go/internal/registry/registry.go` 的 `reservedKeys` → 各运行时等价实现，三处必须一致
3. **Apple ↔ 运行时对齐**：Apple emit 的 JSON 键名必须与运行时读取的 JSON tag 完全匹配
4. **hash 一致性**：`OpCall.unique_name()` 的语义元组中的字段必须与当前 OpCall 上实际有运行时语义的字段保持一致——已废弃的字段不应参与 hash，新增的字段必须加入 hash

端到端验证方法：

- 在 `apple/tests/test_e2e.py` 中编写 Apple DSL → 编译 JSON → Go 引擎执行的用例
- 断言编译产物 JSON 包含正确的键名
- 断言运行时行为符合声明语义（如 strict 字段传 nil → 运行时报错）

教训来源：v0.9.0 将 InputFieldSpec 默认从 Strict 翻转为 Nullable，运行时 JSON 键从 `nullable_common`/`nullable_item` 改为 `strict_common`/`strict_item`，但 Apple DSL 侧未同步——编译器仍 emit 旧键名，运行时静默忽略，Strict 模式声明能力丧失。详见 `memory/reflections/v090-nullable-strict-apple-desync.md`。
