# Apple DSL storage_mode 支持复盘

## 任务

为 Apple DSL 的 `Flow` 构造器新增 `storage_mode` 参数，使编译输出的根级 JSON 可包含 `storage_mode` 字段，供 Go 引擎选择行存或列存 DataFrame。

## 做对了什么

- **最小侵入**：只改了 `Flow.__init__` 新增参数和 `compiler.py` 步骤 9 的条件输出，没有引入新的抽象层。
- **按需输出**：未指定 `storage_mode` 时 JSON 中不出现该字段，兼容旧配置。
- **端到端验证**：用 `compile_dict()` 直接验证有值/无值两种路径。

## 教训

- **根级配置扩展点缺少统一模式**：`storage_mode`、`resource_config` 都是在步骤 9 之后用 `hasattr` + 条件写入。若后续再新增根级字段，应考虑统一为一个可扩展的 root-level config 注入机制，而非逐个 if-block。
- **文档同步要及时**：代码改动在一个 commit 完成，但 llmdoc 同步延迟到了下一轮对话，增加了遗忘风险。

## 文档影响

- `llmdoc/architecture/apple-compiler.md` 步骤 9 需补充 `storage_mode` 输出。
- `Flow` 顶层声明参数列表需补充 `storage_mode`。
