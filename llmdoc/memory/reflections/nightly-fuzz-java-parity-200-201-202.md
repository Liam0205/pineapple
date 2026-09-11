# Nightly diff-fuzz #200 / #201 / #202：三个同周到达、互不相干的 pine-java 分歧

日期：2026-09-11。三个 nightly differential-fuzz issue（2026-09-07 / 09 / 10，各 1 失败、0 unstable、都是 go vs java、cpp 与 go 字节一致），一次任务修完，三个独立 commit + 本文。

## 结论先行

| Issue | 症状 | 根因（全在 pine-java） | 修法 |
|---|---|---|---|
| #200 | 同一值 Go 输出字符串 `"1777288596209286259"`、Java 输出数字 `1777288596209286100` | luaj 3.0.1 `LuaTable.NumberValueEntry.set` 用 `tonumber()` 复用数字槽：同一全局先写 number 再写数字形 string，读回是 number。上游 `b8aaaafb`（2018）已修、从未发版 | `TransformByLua.setGlobal`：新值 TSTRING 且旧槽 TNUMBER → 先置 nil 再写 |
| #201 | 全部 item 顺序不同 | `ReorderShuffle.anyToString` 对 List/Map 用裸 `new ObjectMapper()`，`[28.0,42.0]`/`2.0E100` vs Go `[28,42]`/`2e+100`，salt 字节不同 | 新增 `GoFormat.marshalJson`（复用响应路径的 Go 兼容 mapper），shuffle 与 bench stub 改用 |
| #202 | Go/Java 分页取到不同 item（`id_2` vs `id_20`） | `ReorderSort` 用 `Double.compare`：`-0.0 < 0.0`；Go/C++ 用 `<` 视为相等、稳定排序保原序；三个零恰在 `filter_paginate` 页边界 | 比较器改 `x<y?-1:x>y?1:0` |

三个 fixture 文件各加用例（`reorder_sort.json` 2 例、`reorder_shuffle.json` 3 例（第三例是审计 R1 补的整数字面量 salt）、`transform_by_lua_edge_cases.json` 1 例，另有 `fixtures/server_byte_exact/15` 覆盖响应路径的整数字面量），期望值由 Go 生成，由 **Go 与 Java 两个** fixture runner 消费（`pine-go/integration/fixture_test.go`、`FixtureTest.java`；pine-cpp 没有算子级 fixture runner，它只经 cross-validate 的 `fixtures/pipelines/` 比对——commit `48283ac7` 说明里的「all runtimes」不准确，审计 R1 指出）；Java 单测 `GoFormatMarshalJsonTest`（新，含审计后补的整数载体 / 非有限值 / `/stats` 形状三例）与 `TransformByLuaTypeIdentityTest`（+3 多 item 用例，审计后再 +2 `__newindex` 用例）。每处都做了 red-before（文件备份还原旧源码）/ green-after；审计补的门另做了 mutation 验证（把修法改回审计指出的错误形状，对应用例变红）。

## 过程：哪些动作真正把范围收敛了

**1. 三份统计都说 column，实测 row 也红。** 三份报告 `row=…/0 column=…/1`，第一反应是列存专属（Java 列存 typed column 对数字形字符串做了强转？）。把 artifact 的 `storage_mode` 翻成 `row` 重跑，三个 case 全部照样分歧——这一步花两分钟，省掉了读列存代码的一整个下午。n=1 的分层统计只是提示，不是证据。

**2. #200 的最小复现四次全绿，差别在数据顺序。** 同脚本、同 `item_defaults`、row/column × 有/无默认值四个最小配置都对。按 `guides/ci-quality-baseline.md` 的 artifact triage playbook 从尾部截断原 pipeline，砍到 `recall + op_6` 两个算子仍红 → 触发条件在 recall 数据里。对比后发现原 case 里 item 0 是 null→默认值 784.6（number），item 1 才是大整数字符串；我的最小配置把字符串放在第一位。把「谁在前」当变量做六个顺序变体，一次定位到规律：**数字之后的所有数字形字符串都变数字，直到遇到一个非数字字符串**（`[7, "1777…", "123", "1e5", "a", "456"]` → 中间三个变数字、`"456"` 不变）。这个规律本身就指向「槽位状态」而不是「逐值转换」。

**3. `tostring(item_tag)` 探针证明值进 Lua 之前就已是数字**，把范围从 fromLua 收到 toLua/`globals.set`。然后独立 luaj 探针（不经 pineapple）复现：`t.set("k", 7.0); t.set("k", "1777…"); t.get("k").typename()` = `number`。再 `javap -c` 3.0.1 的 `NumberValueEntry.set` 看到 `tonumber()` 无类型守卫，与 luaj master 源码对照找到修复提交。

**4. #202 从数据直接读出来。** 分歧 item 是 `id_2`（score `0`）与 `id_20`/`id_33`（score `-0.0`），`reorder_sort asc` 后 `filter_paginate page=1 size=16` —— 排序位置 15/16/17 正好是三个零。Java `Double.compare(-0.0, 0.0) = -1`，其余不用猜。

**5. #201 读代码即见**：`new com.fasterxml.jackson.databind.ObjectMapper().writeValueAsString(v)`，而仓库里 Go 兼容 mapper 已经因 #180/#183/#189 修了三轮。它三轮都没被扫到，因为三轮找的都是「进响应的 JSON」，salt 是喂 hash 的中间字节，不在任何响应里。

## 教训（已落稳定文档的只留指针）

- **同一函数的第三个维度**（→ `must/conventions.md`「审计结论只对被审的那个维度成立」新增段）：`TransformByLua` 的 `toLua`/`fromLua` 邻域，#175 查派发谓词、#189/#190 查类型窄化、#200 查槽位跨 item 状态。既有 `inputStringRoundTripsThroughLuaUnchanged` 用一个 item 测 identity，构造上看不见需要两个 item 的状态耦合。凡复用容器（VM 全局表、池化 state、`thread_local` 缓冲）都有「跨调用状态」这一维，单样本测试对它恒绿。
- **红绿检查后要重新编译**（→ `guides/investigation-to-fix-testing.md`「Nightly fuzz artifact 的三步归因」第 3 步）：文件备份还原旧源码跑 red-check、`cp` 回修复版后，`target/classes` 里是 red-check 那次编译的旧 class；紧接着黑盒重跑 artifact 得到「仍然分歧」，差点回头怀疑修法。单测框架自己编译所以看不出来，直接调 CLI 的验证才中招。
- **同一类型的多条序列化路径按「字节去哪」分类**（→ 同上）：grep `new ObjectMapper()` 后按 进响应 / 进 hash 或比较 / 进日志 / 进配置解析 归类，前两类必须走 `GoFormat`。
- **上游已修但从未发版**（→ `guides/investigation-to-fix-testing.md` 新增小节）：fork / classpath 遮蔽 / 桥接层守卫三选一的取舍，以及「守卫只覆盖被字节码证实的那条边、修不到的写进 doc-gaps 并在 javadoc 指过去」。
- **IEEE 负零第三次现身**（→ `must/conventions.md` 新增「数值排序比较按 IEEE `<`/`>`」节）：dedup（hash 相等）、shuffle（已归一）、sort（比较器相等）三处各自要守；`memory/reflections/differential-fuzz-discoveries.md` 记过前两处。

## 顺带发现、未修、已登记（`memory/doc-gaps.md`）

1. luaj 脚本内部同 key 二次赋值（`t.k = 7; t.k = "123"`）同样被 coerce——桥接层修不到的残留。
2. luaj `tostring()`/`..` 对非整数 double 走 float 精度（`784.6283`、`1e100`→`Infinity`、`2^53+1`→整数形），三运行时实测对照已写入。
3. pine-cpp 拒绝手写 config 的 `pipeline_map: null` 与缺 `$metadata` 的算子，Go/Java 接受。
4. pine-go 两个 Lua 后端对宿主写缺失全局是否触发 `__newindex` 不一致（默认 wangshu raw、gopher-lua honour；审计 R2 发现，见 doc-gaps 同名条目）。
5. 非有限值进入复合 shuffle salt 时三方字节三样（Go `%v` fallback / C++ 裸 `nan` / Java 带引号；审计 R6 发现，先于本 range；我把参考对象错当成 `json.Marshal` 而非 Go `anyToString` 含 fallback 的完整行为，见 doc-gaps 同名条目）。

## 未做 / 边界

- 三个修复加审计驱动的第四个修复（整数载体按 Go float64 拼写，`7be3c34f` + 后续把规则从 mapper 移到 payload 层）都只动 pine-java；Go 与 C++ 在三个 case 上本就字节一致，未改。

## 本地盲审查出的、我自己没看见的（按轮次）

- **R1**：`marshalJson` 只对 `Double` 载体等价——Jackson 把请求里的整数字面量解成 `Integer`/`Long`/`BigInteger` 并原样进 frame，而我的 javadoc 写成了无条件等价。这正是 `must/conventions.md` 刚写下的「别让『修了』读成『全修了』」。实测 Go/C++ `[i1,i4,i3,i0,i2]` vs Java `[i0,i4,i3,i2,i1]`，且响应路径同样分歧（`bx15` 之前根本没有覆盖整数字面量 ≥ 2^53 的通道）。
- **R2 阻塞**：我修 R1 时把 Long→float64 装在共享 mapper 上，而 `/stats` 的 `sum_ns`/`total_duration_ns` 是 Go int64、必须精确——commit message 里「此类值都远小于 2^53」这句是**想当然**：那是累计纳秒不是计数，2^53 ns ≈ 104 天。仓库里早有同型先例（`sortedShallow` 为「Go 对 map 排序、struct 不排」把 payload 与 `/stats` 分开处理），我没把它认出来。**判据**：改一个共享序列化器之前，先列出它的全部消费者及每个位置的 Go 类型；Go 规则取决于位置上的 Go 类型，Java 得按位置建模。
- **R6（最终全范围）重要**：`marshalJson` javadoc 写「NaN/Inf 复合值 Go 无字节可匹配」——错把参考对象当成 `json.Marshal` 而非 Go `anyToString` 的完整行为：后者在 Marshal 报错后落 `fmt.Sprintf("%v")` 得 `[NaN 2]` 并照常 hash，C++ 写裸 `nan`，Java 写带引号 `"NaN"`，实测三方三种 shuffle 顺序。R1 报告其实已写明 Go 侧走 `%v`，我选「改文档」选项时把 Go 行为写反了。**判据**：复刻某个 Go 函数时，参考对象是它的调用者在那条路径上的完整行为（含 error 分支的 fallback），不是它调用的库函数。已登记 doc-gaps「非有限值进入复合 shuffle salt」。
- **R2 重要 ×2**：(a) 修 R1-M2 时把守卫分支的两次写改成 `set(NIL)`+`set(value)`，`set(NIL)` 会**删除**键，于是第二次写变成「缺失键写入」、被脚本的 `__newindex` 接走——两个既有测试各测一边（数字→字符串、`__newindex`），交集无覆盖；(b) 我在 commit message 里写「gopher-lua 与 C Lua 都 honour `__newindex`、Java only 绕过」，但 pine-go **默认后端是 wangshu**，其 `SetGlobal` 是 raw 写，实测矩阵：wangshu raw / gopher-lua honour / LuaJIT honour / Java(修后) honour。Go 自己两个后端不一致，「参考运行时怎么做」在这里没有单一答案；选边跟 Lua 语义与 C++ 标杆，wangshu 分歧登记 doc-gaps。**判据**：「Go 也如此」这类断言必须对**默认构建**实测，opt-in 后端不代表 Go。
- fuzz 生成器未改：三个缺陷的触发形状（数字后接数字形字符串、复合值 salt、负零平局落页边界）生成器早已能产出（2026-05/07 起），只是概率低（各约 1/10000 轮），nightly 10k 轮三天各抓一个。fixture 是比调生成器概率更便宜的门。
- 种子重放：`--rounds 362 --seed 2262930939` 与 `--rounds 161 --seed 4071164659` 本地全绿（362/362、161/161）。`--rounds 7172 --seed 1622586423` 单机需约 3 小时（0.7 轮/秒），未纳入本文的完成判据；#201 的修复由原 artifact case 三方字节一致 + fixture 红绿 + `GoFormatMarshalJsonTest` 钉住。
