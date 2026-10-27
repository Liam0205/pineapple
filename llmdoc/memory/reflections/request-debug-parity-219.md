# Issue #219：请求级 Debug 三运行时对等

日期：2026-10-27。分支 `feat/request-debug-219`，基于 v0.12.0（#217 的 `RequestContext` 合入之后）。单会话完成 Java / C++ 实现、三方 probe 与 cross-validate §23。

## 结论先行

| 项 | 内容 |
|---|---|
| 缺口 | Go #216 新增的 `pine-go/pkg/debug`（请求级 `Emitter` / `Recorder`，调度器自动绑定节点元信息）只有 Go 有；Java/C++ 在 #217 之前没有可携带请求级值的上下文，无从接入 |
| 做法 | Java `page.liam.pine.debug`（`NodeMeta` record、`Emitter`、`Recorder`），C++ `pine-cpp/include/pine/debug.hpp`（`pine::debug`，`emit` 取 `const Variant&`）；都建在 `RequestContext.withValue` / `with_value` 上，私有身份键。引擎在节点分派前（含 `data_parallel`）从 run 上下文派生绑定后的上下文 |
| 锁定 | `scripts/cross-validate/23-request-debug.sh` 三方 probe 四场景逐字节（12 个事件行）；Java `RequestDebugTest`、C++ `tests/test_request_debug.cpp` 对照 Go 的两份 debug 测试 |
| 稳定文档 | `reference/operator-contract.md`「请求级 Debug（嵌入 API）」新增三运行时 API 对照表并改名（README 锚点同步）；`architecture/dag-engine.md`、`architecture/pine-cpp-runtime.md`、`design_doc/08` 只留指针 |
| 提交 | Java / C++ 实现各一、三个 probe 各一、cross-validate 一个、docs 一个（不写 SHA） |

## 可复用的判断

**1. 依赖链解开后，先核实「元信息在别的运行时是否已经有」。** #219 被 #217 卡住的只是「上下文能否携带值」；NodeMeta 的三个字段在 Java（`DAG.Node.subFlow`、`config.typeName`）和 C++（`Node::subflow`、`op.type_name`）都早已存在且来自同一展开算法（`Config.expandEntries` / `config.cpp:expand_entries`，父路径都是直接包含它的 `pipeline_map` 条目名）。确认这一点后，实现缩小为「API + 一处绑定」，不用改 DAG 构建。

**2. 对等的是 API 面，不只是行为。** Java 初稿给 `Recorder` 加了 Go 没有的 `meta()` 访问器，行为无害但会成为只有 Java 下游能依赖的东西；提交前删掉。判据：移植时逐个对照 Go 的导出符号，多出来的要么有理由写进对照表，要么不加。

**3. probe 的事件行必须排序，且要有计数守卫。** 节点并发与分片并发让事件顺序不确定，probe 输出排序后才能逐字节比；§23 另外断言 Go 输出恰有 4 个 scenario 行和 12 个 event 行——否则「recorder 根本没送达算子」时三方都打印零个事件，照样字节一致。用变异检查确认守卫承重：把 Java 绑定的 SubFlow 改成空串，§23 报 `java differs from Go`（`mid/probe219_emit/outer/...` 变 `mid/probe219_emit//...`）。

**4. 变异检查后要先重新编译再验「恢复后通过」。** 还原源文件后直接重跑 §23 仍报分歧——跑的是旧 class。这一条 #200/#201/#202 的复盘已经记过，这次又踩了一次，所以在这里重复：还原 → 编译 → 再跑。

## 环境踩坑

- `-DPINE_CPP_WERROR=ON` 打开测试 target 时，现有测试里 `OperatorSchema` 聚合初始化省略 `metadata` 字段触发 `-Wmissing-field-initializers`，28 处（CI 的 strict 构建当时不编测试，所以没暴露）。给 `pine-cpp/include/pine/operator.hpp:OperatorSchema::metadata` 加默认成员初始化器 `{}` 即消除，调用点不用改；CI `cpp-lint` 的 strict 构建随后加上 `-DPINE_CPP_BUILD_TESTS=ON`，测试源同样受 `-Werror` 约束。
- 判断某个文件是否符合 clang-format 时，`--assume-filename` 只对 stdin 生效；传文件路径给 `/tmp` 下的副本会因找不到 `pine-cpp/.clang-format` 而回落默认风格，得出「engine.cpp 不合规」的错误结论。用 `make fmt-check` 或把副本放回仓内再检查。
