# Issue #205：写侧字段名对照声明输出——第一次双会话协作修复

日期：2026-09-13。分支 `fix/205-enforce-declared-output-fields`。第一次以「team-leader（本会话）+ team-member（`pa-reviewer` 独立会话）」协作修一个三方缺陷：leader 做 Go 侧、fixture、文档与全部 git 操作；member 只读调查 → 实现 Java/C++ 镜像，改动限定在各自目录，不 commit。

## 结论先行

| 项 | 内容 |
|---|---|
| 缺陷 | `ApplyOutput` 三方都不对照算子声明的 `item_output` / `common_output`；写未声明字段完全绕过冒险图（无 WAW/WAR/RAW 边） |
| 修法 | 在算子类型方法校验（`ValidateOutput`）**同一位置**追加字段名对照；不改 Frame 接口。Go `types.ValidateDeclaredOutputs` + `scheduler.go` 调用点；Java `OutputContract` + `Engine.java`；C++ `engine.cpp` |
| 契约 | 四条带字段名的写路径全查（`SetCommon` / `SetItem` / `SetItemColumnFloat64` / `AddItem`）；字段名字节序升序去重；common 先查、有违规只报 common；文案 `output contract violation: operator wrote undeclared {common,item} output field(s) [f1 f2]` |
| 锁定 | `fixtures/errors/runtime_undeclared_{item,common}_output.json`，`wrapping_exact_engines: go/java/cpp`，全部用生产算子 `recall_static` 驱动 |
| 破坏面 | Go 全量 13 处失败全是测试专用算子；生产算子零改动；`fixtures/benchmarks/` 全部已声明；用户可见变更仅限使用 `transform_bench_cpu` / `transform_bench_sleep` 却未声明 `_bench_result` / `_bench_slept` 的下游配置 |

稳定文档：`reference/operator-contract.md`「写侧字段名必须在声明的输出里」、`architecture/dag-engine.md`「写侧字段名校验」、`must/conventions.md` 受影响面纪律第六例、`doc/guide_operator{,-en}.md`。

## 过程：issue 里三处事实错误是怎么被纠出来的

issue #205 是我自己在前一天写的。动手前按 `conventions.md`「先实测受影响面」走了两步，两步都改了范围。

**1. 枚举 API 全部方法，而不是从 issue 举的例子推断类别。** issue 写"`SetItem` / `SetCommon`"；`grep 'func (out \*OperatorOutput)'` 得到四条带字段名的写路径，多出 `SetItemColumnFloat64`（#157 加的批量列写）和 `AddItem`。`AddItem` 是四条里最不受控的：recall 逐行交出的 map，键来自配置（`recall_static.items`）或资源数据（`recall_resource`），恰好落在 issue 范围之外。探针 PATH 4 用一个 recall 交出带未声明键的 map，三方都静默落盘。

**2. team-member 独立核对 issue 事实，推翻"内置生产算子都靠构造诚实"。** 我 grep 的形状是 `SetItem(..., "literal")`，只能看见字面量字段名；`recall_static.go:78-87` 是 `for field := range o.setCommon { out.SetCommon(field, ...) }`，字段名是变量，grep 不命中。member 从"字段名从哪来"而不是"有没有字面量"出发，找到 `recall_static` / `recall_resource` 三方六处。**这是「自己写的 issue 不构成豁免」第一次有了机制性的执行方式——第二个人、无我的推理上下文、拿 issue 正文逐条核对。**

**3. `_source` 需要豁免吗？——两种回答都对，取决于校验点。** member 指出引擎自己会往 added item 注入 `_source`（`row_frame.go:258`），任何字段名检查都要豁免它。我实测发现不需要：校验点在 `applyOutput` **之前**，`_source` 在 `applyOutput` **内**注入，正常路径它不会出现在 `addedItems`。但测试跑起来它**确实出现了**——`TestDAGOrder_RepeatStability` 第二轮报 `[_source item_norm item_tag]`。根因是测试 recall 算子把 Init 时缓存的 map 按引用交给 `AddItem`，frame 按引用追加并原地注入 `_source`，下一轮再交出的就是被污染的 map；顺带 `item_norm` / `item_tag` 这两个**下游 transform 写进那一行的字段**也回流到了算子配置里。生产 `recall_static` 交出前拷贝（`static.go:81-85`），免疫。所以正确处置不是豁免 `_source`，而是让测试算子像生产算子一样拷贝——新检查顺带抓出了一条此前无人可见的"交给 `AddItem` 的 map 归 frame 所有"隐式契约违反。判据：**校验里出现引擎注入字段，第一解释是调用方复用了已交出的容器，不是校验点放错了。**

member 在 Java/C++ 侧核实同一件事时发现这条只在 Go 成立：Java 两个 Frame 注入前 `new LinkedHashMap<>(added)`，C++ `add_item` 按值接收——同样的复用在 Java/C++ 不会污染算子。这是扩展 API 上的三方不对等，登记为 `memory/doc-gaps.md`「`AddItem` 交出的 map 所有权」，本次未改 Go 侧行为（recall 是 item 数量级热路径，加一次拷贝要先 bench 归因）。

**4. 读侧投影会掩盖症状，最小复现要让读者声明该字段。** 第一版探针里下游 reader 不声明 `undeclared_item`，读到 `<nil>`——是读侧投影拦住了，看起来"没问题"。真正的危险形状是**写者不声明、读者声明**：读者能读到，但图里没有边。改成这个形状后 40 轮里 reader 全部先于 writer 跑、报字段缺失——这不是竞态偶发，而是无边情况下调度器的稳定顺序恰好反了。

**5. CI 首轮红在 `benchmark` job：`pine-go/benchmarks/` 独立 module 逃过了本地全量验证。** 本地 `go test ./...`、cross-validate 03/05/14、differential-fuzz 100 轮全绿后 push，CI `benchmark` job 报 `BenchmarkParallelRecall` 的两个 `recall_static` 用 `makeItems` 生成四字段却只声明两个。主 module 的 `./...` 不编译子 module，所以它是第一个真正没在本地跑过的消费者。这是该子 module 第三次以不同方式咬到 PR（#166 tidy、#160 文档命令、#205 行为变更），判据已进 `guides/standard-workflow.md` 3c：改运行时行为时本地补一次 `-benchtime=1x` 全量 benchmark。

## 双会话协作的形状（第一次，记下可复用的部分）

- **同一工作树、按目录分域**：leader 只动 `pine-go/**`、`fixtures/**`、`llmdoc/**`、`doc/**`；member 只动 `pine-java/**`、`pine-cpp/**`。全部 `git add` / `commit` 由 leader 做，member 改完只报告。单域提交纪律因此天然成立——每个 commit 的文件集合就是一个人的目录。
- **member 先只读调查、再实现**：调查报告里"纠正 issue 事实"那一节的价值高于全部补充细节。派活时明确写"发现我在 issue 里写错了事实，直接指出来，这比补充细节更有价值"，member 就真的把它放在报告最前面。
- **规则单向定义**：Go 侧定契约（校验位置、四条路径、排序、通道优先级、文案），member 单向对齐，且被要求"发现规则在某方拼不出字节一致，立刻报告、不要自己改规则"。这与 `conventions.md` codegen 单向对齐方向同型。
- **Java 排序要点提前说**：`String.compareTo` 是 UTF-16 code unit 序，与 Go `sort.Strings` 字节序在非 BMP 字符上不一致；派活时直接指到 `GoFormat` 的 UTF-8 比较器（#183 那轮加的），避免 member 走弯路。

## 未做 / 留待

- Apple 编译期对 `recall_static.items` 键 ⊆ `item_output` 的校验：运行时检查已定性，编译期校验能把错误再提前一层，属 follow-up，未做。
- `pine-java/notes/pine-java-gap-analysis.md:66` 把 `ValidateOutput` 描述成"校验输出字段是否超出 metadata 声明"——这从来不是它做的事，是 gap 分析时的误读；该文件是历史笔记，未改。
- #204（DAG 形式化）把本 issue 列为健全性定理的前置修复；修完后定理前提从"假设算子诚实"变为"引擎强制算子诚实"。
