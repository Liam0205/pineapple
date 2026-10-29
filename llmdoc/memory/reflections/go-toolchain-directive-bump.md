---
name: go-toolchain-directive-bump
description: pine-go 加 `toolchain go1.27.3`（2026-10-28，PR #227 一组 commit）复盘：`go` 行与 `toolchain` 行的职责拆分、升 toolchain 连带牵动评审 setup 脚本的 Go 阶段与 golangci-lint 固定版本、评审三次纠正同一组文档表述。
type: reflection
---

# pine-go 升 toolchain go1.27.3 复盘

日期：2026-10-28。范围：`148f4784`、`6bf79041`、`58bdce76`、`b591b3c5`、`fb611a83`、`528ebe41`、`fda1073d`。稳定结论已写入 `guides/ci-quality-baseline.md` 的「Go 版本」与「Agentic workflow 的环境准备脚本」两节，本篇只记过程。

## 做了什么

- `148f4784`：`pine-go/go.mod` 与 `pine-go/benchmarks/go.mod` 各加 `toolchain go1.27.3`，`go` 行留在 1.26.2。CI 的 setup-go 读 `go-version-file`，之前一直跑 1.26.2，本地早已是 1.27。
- `6bf79041`：`.github/agentic/setup.sh` 的 Go 阶段原来按 `go` 行取 minor，`toolchain` 行出现后会选到 1.26.x，第一条 go 命令再由 `GOTOOLCHAIN=auto` 在阶段预算之外下载 1.27.3。改为优先读 `toolchain` 行。同时把固定的 golangci-lint 从 v2.12.2（go1.26.2 构建）升到 v2.15.0（go1.27.0 构建），因为旧版拒绝加载配置。
- `b591b3c5`：runner toolcache 里还没有 1.27，Go 阶段总是走回退分支。阶段末尾的 `go version` 原来在仓库根目录执行，那里没有 go.mod，不会触发下载，只是打印 PATH 上的 Go。改到 `pine-go` 目录执行后，下载落在本阶段、计入本阶段预算，打印的也是 agent 实际会用的版本。
- 其余三个 commit 是 llmdoc 与脚本注释的同步与纠正。

## 教训

1. **升 Go 先分清改哪一行。** `go` 行是对下游的最低版本承诺，抬高它会拖动下游；`toolchain` 行在被依赖时会被忽略。只想让 CI 用新 Go 时只加 `toolchain` 行。commit 用两模块 probe 验证了这一点，比从文档推断可靠。
2. **一个版本号有多个读者时，改它要列全读者。** `pine-go/go.mod` 的 Go 版本被 CI 的 setup-go、评审 setup 脚本的 Go 阶段、golangci-lint 的版本检查三处读取，读法各不相同（setup-go 原生支持 `toolchain`，setup 脚本自己用 sed 解析，golangci-lint 比较 minor）。第一个 commit 只顾及了 CI，另两处是在后续 commit 里才补上的。
3. **「在哪个目录执行」决定 go 命令看到哪个 go.mod。** 根目录的 `go version` 不受 `GOTOOLCHAIN=auto` 影响，于是脚本「验证 Go 版本」那一步实际验证的是另一回事。与 setup 脚本已记录的几条失效模式同族：脚本继续跑、输出看起来正常，行为却与声称的不同。
4. **约束要按实际比较的粒度写。** golangci-lint 的拒绝条件比较的是 minor（`go1.26` 对 `1.27.3`），第一版文档写成「构建它的 Go 不能比 `toolchain` 行旧」，会让人误以为 go1.27.0 构建的版本处理不了 1.27.3。评审指出后改成 minor 粒度（`fb611a83` / `528ebe41`）。
5. **文档描述的是某个 commit 之前的行为时，要跟着那个 commit 改。** `b591b3c5` 把下载挪进 Go 阶段后，guide 里「下载发生在阶段预算之外」的说法变成了错的，又靠一轮评审才改掉（`fda1073d`）。改脚本行为的同一个 commit 里就该 grep guide 里描述这个行为的句子。

## 未证实

升级动机之一是 Go native fuzz 在 `-fuzztime` 边界偶发的 `context deadline exceeded`，commit 只说「预期 1.27 会修好」。没有 CI 数据证实前，按 `ci-apt-resilience-and-dead-weight-packages.md` 的判据处理（无 crash corpus 即 rerun）。
