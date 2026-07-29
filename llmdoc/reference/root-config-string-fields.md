# 根级字符串配置字段的类型契约

JSON 配置根级的字符串字段在三个运行时遵守同一条类型规则。**加新的根级字符串字段时必须按这条规则处理**，否则会立刻产生跨运行时分歧（issue #187 修的就是这个）。

## 规则

| 输入 | 三方行为 |
|---|---|
| 字符串 | 接受 |
| 键缺省 | 接受，取默认值 |
| `null` | 接受，取默认值 |
| 数字 / 布尔 / 数组 / 对象（present 但类型错） | **拒绝**，抛配置错误 |

`null` 与缺省同档，是因为基准侧 pine-go 把 `null` 解到 string 字段是 no-op，零值原样留下。

## 基准与三处实现

规则的定义方是 pine-go，而且**不是某个字段的性质，是 `encoding/json` 的性质**：`RootConfig` 的每个字符串字段都声明成 `string`，任意一个字段类型不对就会让整份 unmarshal 失败。另两侧是为对齐它而写的显式检查。

| 运行时 | 位置 | 机制 |
|---|---|---|
| pine-go | `pine-go/internal/config/types.go`（`RootConfig` struct tag） | `encoding/json` 类型强制，无显式代码 |
| pine-java | `pine-java/src/main/java/page/liam/pine/Config.java`（`rootString`） | `isNull()` 放过、`!isTextual()` 抛 `ConfigError` |
| pine-cpp | `pine-cpp/src/config/config.cpp`（`require_string`） | `is_null()` 返回 `nullptr`、非 string 抛 `ConfigError` |

错误文案**只有 pine-java 与 pine-cpp 相同**（`config field "X" must be a string`）。
pine-go 的拒绝来自 `encoding/json` 整份 unmarshal 失败、经 `Load` 的 `JSON parse error: %v`
包装，文案是 `JSON parse error: json: cannot unmarshal number into Go struct field ...`，
**与另两方不同**。要按文案匹配就只能匹配各自的子串；值白名单那层的文案才是三方逐字节相同的。

## 当前受这条规则约束的字段

`storage_mode`、`log_prefix`、`_PINEAPPLE_VERSION`、`_PINEAPPLE_CREATE_TIME`。

`debug` 是布尔字段，不在此列——**但要注意它仍然是修前那个样子**：pine-go 拒绝错误类型；**pine-java 的 `asBoolean()` 会强转**——`1` 与 `"true"` 都会**真的把 debug 打开**、`"yes"` / `[1]` 强转成 false；pine-cpp 的 `is_bool()` 守卫静默忽略、保持关闭。**三方是三种行为，不是两种**（实测）。也就是说 `debug` 上还留着
本次为四个字符串字段消除掉的那个分歧，「不在此列」是范围声明、不是「已经一致」；`storage_mode` 在类型层之外还有一层值白名单，见 `architecture/dag-engine.md` 的 `storage_mode` 节。

## 新增第五个字段时要同步的四处

1. pine-go 的 `RootConfig` struct tag（声明成 `string` 即自动满足）
2. pine-java 的 `Config.parseRoot`，走 `rootString(root, "...", 默认值)`
3. pine-cpp 的 config 解析，走 `require_string`
4. 类型矩阵测试：`pine-go/internal/config/storage_mode_validation_test.go` 的 `TestRootStringFieldsRejectNonStrings`、pine-java `StorageModeValidationTest`、`pine-cpp/tests/test_storage_mode_validation.cpp` 的字段列表都要加上新字段

第 4 步不是可选的。**「某一侧缺一整块」这种分歧在读代码对比时不可见**：`_PINEAPPLE_CREATE_TIME` 在 pine-java 里**曾经完全不存在**，pine-go 会因它类型错而拒绝整份配置，pine-java 连这个字段都不解析。这一处是穷举矩阵测试（字段 × JSON 类型）里「pine-java 那一格怎么填都不红」暴露出来的，不是读三方解析代码看出来的。补上的 `pineappleCreateTime` 是纯 metadata、不参与任何行为，存在的唯一理由就是让类型规则在四个字段上统一。

## 值层文案的一个转义限定

值层白名单的文案三方逐字节相同，**但只对不含引号／反斜杠／控制字符的值成立**。pine-go 用 `%q`
格式化被拒的值，另两方是裸拼接：`"storage_mode": "co\"lunm"` 在 pine-go 出 `"co\"lunm"`、
另两方出 `"co"lunm"`。非 ASCII 不受影响（`%q` 不转义可打印 Unicode，`cölunm` / `日本` 三方相同）。

可达性低且不改变拒绝与否，只影响文案字节。记在这里是因为三个单测与两份用户文档都无限定地断言了
字节对等，而全 ASCII 的 fixture 碰不到它——与 #183 的 escape-vs-raw 同类。
