# [JSON 数字格式跨运行时对等修复（issue #180）]

分支 `fix/180-go-java-json-number-parity`（基于 `origin/master` = `ab2dfd5f`），两个 commit：
`70762f19 fix(cpp)` + `95fa8f6f fix(java)`。顺带开了 issue #183（未修）。

## Task

修 issue #180：差分 fuzz 报出 Go 与 Java 的 JSON 数字格式分歧（`1e20` → Go 输出
`100000000000000000000`、Java 输出 `1.0E20`）。目标是让三运行时的 JSON 数字字面量
与 Go `encoding/json` 字节一致。

## Expected vs Actual

- Expected：一个边界值的特例。issue 标题写的是 "divergence at 1e20"，读起来像
  Java 少处理了一个阈值，改一处即可。
- Actual：**三个互相独立的缺陷，分布在两个运行时**。用 20 个精选 double 做三运行时
  比对，20 个里 **15 个分歧**。1e20 只是最大那个缺口里的一个点。

## What Went Wrong

### 1. issue 标题把缺陷范围说小了一个量级

三类缺陷各自的真实范围：

- **Java**（`GoFormat.createGoCompatMapper` 里的 Double 序列化器）：只特殊处理
  `-0.0` 和「整数值且在 ±2^53 内」两种情况，其余全部落到 Jackson `writeNumber`，
  按 `Double.toString` 格式化。Go 的 plain-decimal 区间一直延伸到 1e21，所以
  **2^53 到 1e21 整个区间**都错，`< 1e-6` 的小数也全错拼法。
- **C++ 缺陷一**（`pine-cpp/src/config/json.cpp` `go_format_json_number`）：
  `std::to_chars` 配 `chars_format::fixed` 打印的是**精确值**，而 Go
  `strconv.FormatFloat(d, 'f', -1, 64)` 的 precision=-1 意为「最短往返」。
  `1.0000000000000002e20` → C++ `100000000000000016384`、Go
  `100000000000000020000`。
- **C++ 缺陷二**：`chars_format::scientific` 把指数补到两位（`1e-07`），Go 的
  `encoding/json` 会去掉一个前导零。

如果照标题只改 1e20 一处，会留下 14 个分歧。

### 2. 「哪个 `to_chars` 模式是最短往返」反直觉，试错两轮

第一轮假设「不带 format 参数的默认 `to_chars` overload 就是最短往返」，据此改完
实测仍有 1 个分歧。探针结果：

```
d = 1.0000000000000002e20
to_chars 默认              -> 100000000000000016384      (精确值，不是最短往返)
chars_format::fixed        -> 100000000000000016384
chars_format::scientific   -> 1.0000000000000002e+20     (最短往返)
```

**默认 overload 在量级大到不需要指数时会退化成精确打印**，只有 `scientific` 保证
最短往返。最终方案：统一从 `scientific` 取数字，再自己摆小数点
（`go_json_to_fixed` / `go_json_to_scientific` 共用同一个数字来源）。

### 3. Go 指数格式有不对称，必须实测

`strconv.FormatFloat(d, 'e', -1, 64)` 输出 `1e-07`，而 `encoding/json` 输出
`1e-7`。实测确认规则是**只对负指数去掉一个前导零**：

```
strconv 'e': 1e-07  -> json: 1e-7
strconv 'e': 1e-09  -> json: 1e-9
strconv 'e': 1e+21  -> json: 1e+21     (不动)
strconv 'e': 1e+100 -> json: 1e+100    (不动)
strconv 'e': 1e-100 -> json: 1e-100    (三位，不动)
```

靠推理一定写错（容易写成两边都 trim，或干脆不 trim）。两处实现的注释都写明
"verified against encoding/json rather than inferred"。

### 4. 同一个 double 在仓库里有多条格式化路径

pine-java 有 `GoFormat.sprint` / `formatFloatF` / `formatG` 三个格式化器，
**JSON 输出路径一条都没用**——它走 `createGoCompatMapper` 里装的 Jackson Double
序列化器。而 `formatFloatF` 对 1e20 本来就会给出正确答案（内部就有
`BigDecimal.toPlainString` 平铺逻辑），只是 JSON 路径从来没调它。

即：同一个 double，仓库里有两套独立实现给出不同字符串。本次新增的
`formatJsonNumber` 是第四条，所以测试里加了 `formatJsonNumberMatchesTheSerializer`
钉住「序列化器不得持有规则的第二份拷贝」。

`llmdoc/architecture/dag-engine.md`「跨运行时格式兼容（GoFormat）」节列了三个
格式化器和消费者清单，**完全没提 JSON 输出路径**。读文档的人会以为 GoFormat 是
格式化的单一事实源——这个文档缺口正是缺陷能长期存在的条件。

### 5. 校验层：#180 被抓到是侥幸，机制比预想的窄（已核实，纠正原推测）

原先的推测是「fuzz 有另一条不归一化的字节通道」。核实后不是。

`scripts/differential-fuzz.py:1257` 的 `normalize_json` 做
`json.loads` → `_normalize_value` → `json.dumps(sort_keys=True)`。#180 能被报出来
的真实原因是 **Python 的 int/float 类型分裂**：

```
Go   输出 100000000000000000000 -> json.loads 得到 int   -> _normalize_value 不动
Java 输出 1.0E20                -> json.loads 得到 float -> re-dump 成 1e+20
```

`_normalize_value` 只对 `float` 分支做 `round(v, 10)` 和小量级归零，`int` 原样
穿过。所以只有**至少一侧输出整数形状字面量（无小数点无指数）**时分歧才可见。
实测各类分歧在归一化后的可见性：

```
Go 100000000000000000000  vs Java 1.0E20                 -> 可见
Go 100000000000000020000  vs C++  100000000000000016384   -> 可见
Go 9007199254740992       vs Java 9.007199254740992E15    -> 可见
Go 0.0000001              vs Java 1.0E-7                  -> 不可见
Go 0.000001234            vs Java 1.234E-6                -> 不可见
Go 1e+21                  vs Java 1.0E21                  -> 不可见
C++ 1e-07                 vs Go   1e-7                    -> 不可见
1.2345678901234567        vs      1.2345678901234568       -> 不可见（round 10 抹掉）
```

也就是说 15 个分歧里 fuzz 结构上只能看见其中一部分，且看见的那部分靠的是
Python 类型系统的副作用，不是设计出来的检出能力。

cross-validate 侧的两条通道也都没拦住：

- `scripts/cross-validate/09-raw-byte.sh` 标题写 "no normalization"，但字节比较
  失败后会回落到 `normalize_json` 再比一次，相等就**记 `[W]` 警告并计为 pass**。
  key 顺序差异因此被有意容忍——这就是 #183 长期不可见的直接原因。
- `scripts/cross-validate/14-byte-exact-execute.sh` 是真正的字节通道（`curl` 响应
  体直接 `==`），但只有 4 个 fixture。其中 `04_number_precision.json` 名字看起来
  正好覆盖本缺陷，实际输入是 `100000 / 1000001 / 0.5`，×2 后全部落在
  ±2^53 内的整数值区间——**恰好是 Java 旧代码唯一处理对的那个区间**。

结论：**声称「字节级对等」的校验，实际上在归一化之后比较**，校验强度与声明不符。
修 #183 之前必须先决定字节通道怎么补，否则修完没有回归门。

### 6. 顺带发现 #183，判为不同缺陷、单独开 issue

用 600 个 double（边界值 + 随机 bit pattern）做三运行时字节比对：go 与 cpp 字节
完全相同（md5 `bf229ccbf23c82e75ac1beaa20991bc7`），java 字节数相同（18669）但
md5 不同。逐字段查完发现 **600 个数字字面量全部一致**，差异纯在 object key 顺序
（Go `encoding/json` 排序 vs pine-java Jackson 序列化 `LinkedHashMap` 保留插入
顺序）。在干净 master 的 worktree 上重建 pine-java 复现，确认既存且与数字格式无关，
故开 #183，**本次未修**。

（正面教训：字节不同 ≠ 正在修的东西还没修好。先定位差异落在哪个维度，再决定归属。）

### 7. 三个环境坑

- `mvn -o test -Dtest=GoJsonNumberParityTest` 挂在 JaCoCo：`Unsupported class file
  major version 70`（JaCoCo 0.8.13 不认当前 JDK 的 class 文件）。改走
  `make java-test` 全量跑正常。**单测过滤路径与 make target 走的不是同一套 profile，
  前者失败不代表测试有问题。**
- 临时文件用 `/tmp/g.json` 这种极短名踩到 `Permission denied`（`/tmp` 下已有其他
  用户的同名文件）。应用 `mktemp -d` 或带任务前缀。
- 有一次把 java stderr 里的 `[pine:debug]` 行一起重定向进输出文件，JSON 解析失败，
  误以为 java 还在输出 `1.0E20`。**比对输出前先确认捕获的是纯净 stdout。**

## Root Cause

1. **范围来自实测，不来自 issue 标题。** 标题给的是「症状的一个实例」，不是「缺陷
   的范围」。本次先写 20 值探针才发现 15/20 分歧；先扫边界空间再动手是必需步骤，
   不是可选的谨慎。
2. **格式化规则的第二份拷贝。** JSON 序列化器自带一套 double 处理逻辑，而不是调用
   已有的格式化模块，两份实现独立漂移。文档把 GoFormat 描述成单一事实源、却没列
   JSON 路径，让这个分裂在读文档时不可见。
3. **跨语言标准库「等价函数」的隐含语义不等价。** `to_chars` 默认 overload 与
   `FormatFloat(-1)` 名义上都是「合理的默认」，实际一个是精确值一个是最短往返；
   `strconv 'e'` 与 `encoding/json` 的指数补零规则也不同。这类差异只能实测。
4. **校验是按「解析后的对象」比的，声明是「字节级」。** 归一化（`sort_keys` +
   `round(v,10)` + int/float 分裂）把 key 顺序整维度、以及一部分数字拼写差异
   直接抹掉。真字节通道存在但 fixture 覆盖太窄，且那个名叫 `number_precision` 的
   fixture 恰好只覆盖已经对的区间。

## Missing Docs or Signals

- `architecture/dag-engine.md` 的 GoFormat 节没有 JSON 输出路径。缺的信号正是
  「格式化不止这三个入口」。
- 没有任何稳定文档记录「C++ 只有 `chars_format::scientific` 保证最短往返」和
  「Go json 指数负号不对称」。下一个碰数字格式化的人会重新试错两轮。
- `guides/ci-quality-baseline.md` 的 differential-fuzz 节描述了归一化机制，但没有
  写清**归一化抹掉了哪些维度**（key 顺序、数字拼写、float 第 11 位起的差异），
  也没有把 09-raw-byte 的 `[W]` 降级和 14-byte-exact 的 fixture 覆盖面写成一张
  「哪条通道能钉住哪个属性」的表。这与 `guides/cross-layer-validation.md` 已有的
  「fixture 比对器语义决定该层能钉住的属性」是同一条原则，只是没落到 fuzz 上。

## Promotion Candidates

- **进 `guides/`（或 `reference/`）：跨语言数值格式化的两条实测事实**——C++
  `std::to_chars` 只有 `chars_format::scientific` 保证 shortest round-trip
  （默认 overload 与 `fixed` 在大量级下退化为精确打印）；Go `encoding/json` 相对
  `strconv.FormatFloat('e', -1)` 只 trim 负指数的一个前导零。两条都必须实测，
  注释里要留「verified against X rather than inferred」。
- **必须修稳定文档：`architecture/dag-engine.md` 的 GoFormat 节**——补第四个入口
  `formatJsonNumber` 及其消费者（`createGoCompatMapper` → `RunCli` / `PineServer`），
  并写明四者阈值不同、不可互换；同时写明 JSON 路径不走 `formatFloatF`。
- **进 `guides/ci-quality-baseline.md`：校验通道能钉住的属性表**——differential-fuzz
  归一化抹掉 key 顺序与部分数字拼写（含 int/float 分裂导致的检出偏斜）；
  09-raw-byte 把 key-order-only 差异降级为警告；14-byte-exact 是唯一真字节通道但
  只有 4 个 fixture。配一条纪律：**声称字节级对等的属性，必须有一条不归一化的
  通道覆盖**。
- **进 `memory/doc-gaps.md`：字节级对等校验缺口**——待决策项，是给 fuzz 加不归一化
  字节通道，还是扩 `fixtures/server_byte_exact/`，还是取消 09 的 `[W]` 降级。
  这个决策是 #183 的前置条件。
- 不提升：JaCoCo `-Dtest` 坑、`/tmp` 短名、stderr 污染三条留在本篇即可；若再现
  第二次再考虑进 `guides/standard-workflow.md`。

## Follow-up

1. 修 #183 之前先决定字节通道方案（见上述 doc-gap 条目）。#183 的实现层还有一个
   已记录的陷阱：Go 的 sort 按 UTF-8 字节序、Java `String.compareTo` 按 UTF-16
   code unit，BMP 外字符（surrogate pair）会分歧，必须显式给字节序 comparator，
   否则只是把分歧点从 ASCII 挪到 emoji。
2. 给 `fixtures/server_byte_exact/` 补一个真正跨区间的数字 fixture（覆盖
   2^53~1e21 plain-decimal 段、`< 1e-6` 科学计数段、`>= 1e21` 段、1e-7/1e-9 指数
   trim、三位指数），当前 `04_number_precision.json` 只覆盖了原本就对的区间。
3. 调用 `recorder` 落地上面三处稳定文档修改（dag-engine GoFormat 节、
   ci-quality-baseline 通道表、guides 数值格式化事实），并在 doc-gaps 开条目。
4. #183 的 pine-cpp 侧未测（只对了 go/java 这一对），需补测三方 key 顺序。

## 验证情况（本次已完成）

- 20 值定向探针：三运行时 0/20 分歧（修复前 15/20）
- 600 值广谱探针（边界 + 随机 bit pattern）：go 与 cpp 字节全同
  （md5 `bf229ccbf23c82e75ac1beaa20991bc7`）；三方数字字面量 0 分歧
- #180 原始 artifact（`.code-review/artifacts/issue180-divergence-000664/`）重跑：
  三运行时归一化文档 md5 全同（`3bd709debbee27ab8e384a7397d3a6eb`）
- `make cpp-test` 246 用例；`make java-test` 322 用例（新增 7）；`make lint`、
  `make codegen-check`、`make test` 全过
- `make cross-validate` 55/55；`make differential-fuzz` 1000/1000
  （row=604/0 column=396/0）
- mutation 双向验证：C++ 两个机制各自独立变红（改回 `chars_format::fixed` →
  最短往返断言红；去掉指数 trim → `1e-7`/`1e-9` 断言红）；Java 序列化器改回
  `writeNumber` → 7 个测试全红
