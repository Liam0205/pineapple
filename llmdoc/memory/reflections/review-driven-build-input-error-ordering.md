# [评审驱动的 build_operator_input 报错顺序回归修复]

## Task
- 在 `chore/bench_and_doc` 分支上核实并修复一份本地代码评审意见（`.code-review/from-2a22de1/from-2a22de1-to-eb393d5.md`，结论 REQUEST_CHANGES）提出的两处问题：pine-cpp `build_operator_input` 的输入字段校验报错顺序对等回归，以及 `scripts/cpp-tsan-smoke.sh` 的死变量。
- 两项均核实属实并修复，重点是前者——它是一个穿越锁优化战役、revert 之后仍存活到 HEAD（eb393d5）的静默错误对等回归。

## Expected vs Actual
- Expected：三运行时 `build_operator_input` 的输入字段校验顺序应一致——`strict_common → nullable_common → strict_item → nullable_item`（"先 common 后 item"）。这正是 Go/Java（`RowFrame.BuildInput` / `ColumnFrame.BuildInput` / `DataFrame.buildInput`）以及 base commit `2a22de1` 时 C++ 的行为。当一个算子同时违反 strict common 与 strict item 时，规范的首个错误应是 common 错。
- Actual：锁优化 commit `eab4415`（"collapse build_operator_input read locks into a single window"）为把 strict_common + nullable_common + nullable_item 收进**一个** `with_read_lock` 共享锁窗口（降低 N×M 次取锁），把自带取锁的 `validate_strict_items`（因 `shared_mutex` 非递归，必须在窗口外）挪到了**窗口之前**。顺序因此翻转为 `strict_item → strict_common → nullable_common → nullable_item`——strict_item 被提到了 common 之前。后续 per-call 锁形态 revert（`3c87bd6`）保留了这个单窗口结构，回归一路存活到 HEAD。结果：当 common 与 strict item **同时**违反时，新版 C++ 先抛 item 错（`required field "X" is nil on item[i]`），Go/Java 先抛 common 错（`required field "Y" is nil in common`），违反了字节级错误对等契约。

## What Went Wrong
- **性能重构顺带翻转了错误语义，且 perf review 未回头复核 error-parity**：`eab4415` 的目标纯粹是降低取锁次数，但因 `validate_strict_items` 必须移到锁窗口外而隐式改变了四阶段的相对先后。锁窗口合并的正确性论证只覆盖了"数据读取一致"，没有覆盖"多阶段校验的先后顺序一致"。
- **唯一相关 error fixture 只覆盖单一违反**：`fixtures/errors/runtime_build_input_missing_field.json` 只断言 common 缺失、item 正常的单违反场景，因此 cross-validate 的 `05-error-parity` 与 `14-byte-exact-execute` 都抓不到 common-vs-item 的首错分歧。回归对所有现有防护都是不可见的。
- **构造新 fixture 时踩了 flow_contract 前置拦截的坑**：第一版把违反字段写进了 `flow_contract.common_input/item_input`，结果 Go 在 `pine-go/pine.go` 的 `Engine.Execute` 里**先**用 flow_contract 校验 request（`missing required common input field "c"`），根本到不了 `BuildInput`，fixture 测的根本不是目标阶段。修正：违反字段不能进 flow_contract，必须让算子在 `$metadata` 里声明而 flow_contract 留空（与现有 `runtime_build_input_missing_field.json` 同样套路）。
- **陈旧文档恰好与回归后的错误行为吻合，掩盖了问题**：`llmdoc/architecture/pine-cpp-runtime.md:111` 把 `build_operator_input` 描述为"先做 strict 字段批量校验"，既没点明四阶段顺序，又正好和回归后的 item-first 行为一致——文档非但没充当纠偏信号，反而像是在背书错误行为。

## Root Cause
- 错误对等契约此前只被建模到"在哪个阶段报错 + 最终错误文本"两个维度，**没有把"同阶段/多违反同时发生时首个报错的判定顺序"显式列为契约的一部分**。于是锁优化时无人意识到调整 `validate_strict_items` 的位置会触碰外部契约。
- error fixture 的覆盖思维停在"每条校验路径各测一次单违反"，没有"多条校验路径同时违反、断言谁先报"的用例，导致跨运行时报错优先级分歧是结构性无防护的。
- 锁优化战役（见 `bench-lock-optimization-campaign.md`）的复盘聚焦性能面与 fixture 代表性，对 `eab4415` 顺带翻转报错顺序这一副作用毫无记录——副作用直到三周后的代码评审才被发现，说明性能改动触及校验路径时缺少一条强制的语义回归检查项。

## 修复（已落地、已验证）
1. **恢复顺序**（`pine-cpp/src/dataframe/operator_input.cpp` 的 `build_operator_input`）：窗口1 做 strict_common + nullable_common；窗口外做 `validate_strict_items`（`shared_mutex` 非递归，必须独立取锁）；窗口2 做 nullable_item。最终恢复 `strict_common → nullable_common → strict_item → nullable_item`，并在源码注释中固化该顺序。热路径（nullable_item × N 行 × M 字段）仍是单窗口，性能收益保留。
2. **新增双违反回归 fixture** `fixtures/errors/runtime_build_input_common_before_item.json`：算子同时声明 strict_common `["c"]` 与 strict_item `["x"]`，请求 `{"common":{}, "items":[{}]}` 同时违反两者，断言 `wrapping_exact` = `pine: execution error in operator "build_input_common_before_item": required field "c" is nil in common`（common 胜出），`wrapping_exact_engines: [go, java, cpp]`。已用 Go 与修复后 C++ 实跑确认一致；补上 `c` 后单 item 违反才报 `x is nil on item[0]`，反证双违反真实存在、回归版会输出 item 错从而被该 fixture 拦截。
3. **删除 tsan 死变量**（`scripts/cpp-tsan-smoke.sh` 顶部的 `PARALLEL`/`ITERATIONS`）：重构后实际循环改用 per-spec `iters`/`par`，环境变量覆盖已静默失效。
4. 验证：C++ 全套 doctest 211 cases / 110133 assertions 全过；clang-format 合规。

## Missing Docs or Signals
- `llmdoc/architecture/pine-cpp-runtime.md:111` 应从模糊的"先做 strict 字段批量校验"改写为精确的 `strict_common → nullable_common → strict_item → nullable_item` 四阶段顺序，并标注其为对等契约（而非 C++ 内部可自由发挥的实现细节）。
- `llmdoc/architecture/pine-cpp-runtime.md` 的"错误处理必须字节级一致"维度缺一条：**同阶段多违反同时发生时的首个报错优先级（common 先于 item）同样是外部契约**。当前文档只覆盖"报错阶段 + 错误文本"，缺了"首错判定顺序"这一维度。
- 方法名陈旧：`llmdoc/architecture/pine-cpp-runtime.md:112` 与 `llmdoc/reference/operator-contract.md:319` 写成 `batch_validate_strict_items(fields, op_name)`，实际是 `validate_strict_items(fields)`（返回 `pair<bad_field, bad_row>`，无 `op_name` 参数）。
- 流程信号（更适合留在 memory）：构造 error fixture 要触达某一校验阶段，必须先确认前置阶段（flow_contract 请求校验）不会先行拦截——违反字段应进算子 `$metadata` 而非 flow_contract。

## Promotion Candidates
1. **校验/报错的"顺序"是字节级对等契约的一部分**，不只是"报不报错、报什么文本"。应在 `pine-cpp-runtime.md` 的"错误处理必须字节级一致"维度显式加入"同阶段多违反时的首个报错优先级（如 common 先于 item）"，并可在 `must/conventions.md` 的"跨引擎能力等价审计维度"中点名"校验顺序/首错优先级"为对齐范畴。
2. **error fixture 必须覆盖"多违反同时发生"的报错优先级**，而不只是单一违反——否则跨运行时首错分歧是无防护的静默回归。这是 `cross-layer-validation` / 测试纪律的促进候选（可与 `review-driven-resource-lookup-fixes.md` 的跨层追踪清单合并为一条检查项）。
3. **性能重构若触及校验路径（锁窗口合并、批量化、阶段重排），必须回归校验报错顺序**：perf review 的检查项里应新增一条 error-parity 复核，专门确认阶段相对先后未被锁/批量化改动隐式翻转。
4. 交叉引用并补全既有反思 `llmdoc/memory/reflections/bench-lock-optimization-campaign.md` 的**盲点**：它详尽复盘了锁优化的性能面与 fixture 代表性，但完全没记录 `eab4415` 锁窗口合并顺带翻转了 `build_operator_input` 报错顺序这一副作用——该副作用直到三周后的代码评审才暴露，是"性能正确性论证只覆盖数据读取、未覆盖校验顺序"的典型案例。

## Follow-up
- 由 recorder 落地上述文档修正：精确化 `pine-cpp-runtime.md:111` 的四阶段顺序、补"首错优先级"为对等契约、订正 `validate_strict_items` 方法名（`pine-cpp-runtime.md:112` + `operator-contract.md:319`）。
- 在 `bench-lock-optimization-campaign.md` 追加一句交叉引用，指明 `eab4415` 的报错顺序副作用及本反思的修复出处。
- 后续把"perf 改动触及校验路径需复核 error-parity"与"error fixture 需含多违反优先级用例"沉淀进 `cross-layer-validation` 指南的检查清单。
