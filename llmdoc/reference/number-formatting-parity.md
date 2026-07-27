# 跨语言数值格式化实测事实

本文件记录复刻 Go 数值格式化时**必须实测、不能靠推理**的两条事实。两条都是 issue #180 期间用探针实测得到的，各自让一轮实现失败过。

适用场景：在 pine-cpp / pine-java（或任何第四运行时）里复刻 Go `strconv` / `encoding/json` 的 double 输出，或修改已有的格式化路径。

参照的实现落点：

- pine-java：`pine-java/src/main/java/page/liam/pine/GoFormat.java`（`formatJsonNumber`）
- pine-cpp：`pine-cpp/src/config/json.cpp`（`go_format_json_number` / `go_json_to_fixed` / `go_json_to_scientific`）
- Go 侧规则来源：`encoding/json` 的 `floatEncoder`

## 事实一：C++ `std::to_chars` 只有 `chars_format::scientific` 保证最短往返

Go `strconv.FormatFloat(d, 'f'|'e', -1, 64)` 的 `precision = -1` 意为「能往返的最少位数」。C++ 侧对应关系不是逐个 format 平移：

- `chars_format::fixed` 打印的是**精确值**，不是最短往返
- **不带 format 参数的默认 overload，在量级大到不需要指数时也退化成精确打印**——这条最反直觉，issue #180 第一轮就是按「默认 overload = 最短往返」写的，改完仍剩 1 个分歧
- 只有 `chars_format::scientific` 保证最短往返

实测（`d = 1.0000000000000002e20`）：

```
to_chars 默认 overload     -> 100000000000000016384      精确值
chars_format::fixed        -> 100000000000000016384      精确值
chars_format::scientific   -> 1.0000000000000002e+20     最短往返
Go FormatFloat(d,'f',-1,64)-> 100000000000000020000
```

因此 pine-cpp 的 `go_format_json_number` 统一从 `chars_format::scientific` 取数字串，再由 `go_json_to_fixed` / `go_json_to_scientific` 自己摆小数点：两种输出形式（平铺小数 / 科学计数）**共用同一个数字来源**，避免两条形式各自取数字导致的精度分叉。

## 事实二：Go `encoding/json` 的指数格式相对 `strconv` 有一处不对称

`strconv.FormatFloat(d, 'e', -1, 64)` 把指数补到至少两位（`1e-07`），而 `encoding/json` **只对负指数去掉一个前导零**，正指数与三位数指数一概不动。

实测：

```
strconv 'e': 1e-07  -> json: 1e-7      去掉一个前导零
strconv 'e': 1e-09  -> json: 1e-9      去掉一个前导零
strconv 'e': 1e+21  -> json: 1e+21     不动
strconv 'e': 1e+100 -> json: 1e+100    不动
strconv 'e': 1e-100 -> json: 1e-100    三位数，不动
```

靠推理很容易写成两边都 trim 或都不 trim，两种都错。这条规则不对称到没法从文档反推，必须跑一次 Go 程序对照。

## 纪律

两处实现的注释都显式标注了 "verified against encoding/json rather than inferred"。给这类跨语言等价函数写实现时，注释要说明**依据是实测还是推理**——下一个改这段代码的人需要知道哪些常量不能靠「看起来更合理」去调整。

更一般的判据：跨语言标准库里名字/角色对应的「等价函数」，其隐含语义常常不等价（`to_chars` 默认 overload 与 `FormatFloat(-1)` 名义上都是「合理默认」，实际一个精确值一个最短往返）。这类差异只能实测。

## 相关

- 格式化入口划分与消费链：`llmdoc/architecture/dag-engine.md`「跨运行时格式兼容（GoFormat）」节
- 哪条校验通道能钉住字节级数字格式：`llmdoc/guides/ci-quality-baseline.md`「校验通道能钉住的属性」节
- 完整过程记录：`llmdoc/memory/reflections/json-number-format-parity-180.md`
