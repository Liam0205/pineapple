# Pine-C++ 运行时架构

本文档记录 Pineapple 第四运行时 pine-cpp 的定位、契约目标、MVP 边界和当前确定的关键设计决策。

## 适用范围

当任务涉及以下内容时优先阅读本文档：

- `pine-cpp/` 目录内的实现
- Pineapple 第四运行时的架构、性能与可维护性取舍
- C++ 端的 CLI / render_dag / server 入口
- 多运行时 parity 的 fixture / golden / 错误输出契约

## 定位

pine-cpp 的目标不是“再做一个能跑的第四运行时”，而是成为 **在完全对等前提下的标杆实现**。

这意味着它必须同时满足两类要求：

1. **工程要求**
   - 与现有运行时在功能、错误处理行为、错误包装方式、最终报错文案上完全对等
   - 最终完整接入 cross-validate
   - 代码组织与测试分层可长期维护，而不是靠大量特判堆出来

2. **实现上限要求**
   - 在列存、字符串存储、Lua bridge、COW、arena、并行调度等热路径上追求更高实现质量
   - 反过来为 Go / Java / Python 运行时提供可借鉴的设计参考

## 对等契约

### 错误处理必须字节级一致

对 pine-cpp 来说，错误输出不是“语义差不多即可”的内部实现细节，而是外部契约的一部分。

需要对齐的维度包括：

- 是否报错
- 在哪个阶段报错（config load / DAG compile / execute）
- 错误类别
- 错误包装方式
- 最终错误消息文本
- Lua 抛错时的外层包装与上下文格式
- JSON number / int / float 边界导致的报错差异

### 标准优先级：fixture 第一，Go 次之，人工裁决用于收敛

pine-cpp 的行为基准按以下顺序确定：

1. **fixtures / golden expectations**
2. **Go 运行时**（历史主参考实现）
3. **人工裁决**（用于发现分歧后的收敛）

理想状态下，这三者不应长期并存多个真相来源，而应持续收敛为同一套标准。

### Schema / 配置解析严格复刻现有行为

pine-cpp 不单独“变聪明”。在以下方面优先复刻现有对外行为：

- 未知字段是否忽略
- 缺省值如何补齐
- 空字符串 / `null` / 缺失字段如何区分
- `int64` / `float64` / JSON number 的边界处理
- 错误发生阶段与报错路径

如果未来要统一调整规则，应四个运行时一起改，并同步更新 fixtures。

## MVP 边界

pine-cpp 的 **开发期 MVP** 定义为：

1. `pineapple-cpp-run -config ... -request ...`
2. `pineapple-cpp-render-dag -config ... -format dot|mermaid [-collapse N]`

其意义是先打通两条最核心的链路：

- **配置编译 + DAG 构建**
- **执行语义 + JSON 输出**

MVP 允许暂不实现 `/execute`、`/stats`、热加载等外围能力；但 **在提交正式 PR 之前，必须补齐到与现有运行时完全对等**，包括：

- HTTP server
- `/execute`
- `/stats`
- metrics pre-init / parity
- 完整 cross-validate 覆盖

## 关键设计决策

### 1. 对外接口

- 先提供 **C++ API**
- public API 保持窄而简单，为未来增加 **C ABI** 留出空间
- 不把复杂模板与 STL 容器直接暴露为长期稳定的 ABI 契约

### 2. 构建与语言标准

- **CMake**
- 目标标准：**C++23**
- 错误返回采用 `std::expected<T, E>`；若目标编译器过旧，可用 `tl::expected` 作为兼容层
- JSON 库选择 **nlohmann/json**（边界层读写一体，JSON 不在主要热路径）

### 3. Lua 集成

- 选择 **LuaJIT**
- 保持 Lua 5.1 语义，与现有脚本公共子集兼容
- 重点收益来自 tracing JIT 与更低解释器分发成本

### 4. 内存与对象生命周期

- **Arena + RAII 分层共存**
- 长生命周期对象（Engine、配置、LuaJIT state）由 RAII 管理
- 请求执行期间的中间对象以 arena 分配为主
- 对象 API 与分配策略解耦，采用接近 Protobuf Arena 的模式

### 5. DataFrame 与数据表示

- DataFrame 内部采用 **强类型列**，而不是 cell-level 动态 variant
- 动态值 `Value` 只存在于边界层：JSON 解析、Lua 交互、operator config、最终 JSON 序列化
- nullable 表示使用 **`Column<T> + validity bitmap`**
- 字符串底层采用 **arena/string pool** 持有，读取接口暴露 `std::string_view`

### 6. 存储模式语义

- JSON / DSL 层继续接受 `storage_mode: row | column`
- 默认值与现有运行时保持一致（`"row"`）
- **MVP 阶段内部统一采用列式执行表示**
- 也就是说：逻辑上支持 row/column，**物理 RowStorage 在 MVP 阶段尚未实现**

### 7. COW 与 Operator 交互

- 初期采用 **列级 COW**
- `shared_ptr<const Column>` 共享列，写时 clone 单列
- C++ 侧依靠 `const` 约束避免绕过 COW 直接写共享列
- 后续如需要进一步压榨性能，可在算子注册层补 `InputMode`（ReadOnly / Mutating）声明

### 8. 并行执行模型

- 固定大小线程池
- DAG 分支并行与算子内数据分片共用同一个 pool
- 不依赖 C++20 coroutine runtime；当前阶段优先成熟、可控、易调试的线程池方案

### 9. 扩展机制

- 与现有运行时一致，采用 **编译时注册**（schema + factory）
- Lua 作为轻量级脚本扩展口
- 不在第一阶段引入 `.so/.dll` 动态插件 ABI

### 10. 代码组织

推荐结构贴近 pine-go 的模块边界，但采用 C++ 生态更自然的工程壳子：

- `include/pine/` — 对外 API
- `src/config/`
- `src/dag/`
- `src/dataframe/`
- `src/registry/`
- `src/runtime/`
- `src/render/`
- `src/lua/`
- `operators/<category>/`
- `server/`
- `cmd/pineapple-run/`、`cmd/pineapple-render-dag/`、`cmd/pineapple-server/`

核心原则：按 **解析 → 编译 → 执行 → 渲染 → 对外入口** 的生命周期拆模块，而不是一开始就过度抽象出大量“接口层”。

## 测试策略

pine-cpp 采用 **三层测试 + sanitizer**：

1. **单元测试**
   - config 解析
   - sequence expansion / DAG build
   - DataFrame / COW / validity bitmap
   - render_dag 输出
   - `std::expected` 错误传播

2. **集成测试**
   - engine execute 全链路
   - 内置 operator 组合
   - DAG 分支并行 / 算子内分片
   - row/column 语义一致性
   - 后续补 `/execute`、`/stats`

3. **cross-validate / E2E**
   - 最终权威标准
   - 判定 pine-cpp 的外部可观察行为是否已与其他运行时完全一致

4. **sanitizer**
   - ASan
   - UBSan
   - TSan

C++ 端的内存与并发错误不属于“锦上添花”的测试维度，而属于正确性本身。

## 性能工作优先级

性能优化的顺序应保持务实：

1. **先保证 parity**
2. **再优化执行热路径**
   - typed columns
   - LuaJIT bridge
   - string storage / string pool
   - COW
   - validity bitmap
   - arena
3. **再优化并行调度**
4. **最后才看 JSON / render / server 边界层**

原因是 Pineapple 的主要成本几乎肯定在 operator 执行、Lua 表达式求值和 DataFrame 操作上，而不在配置解析和 HTTP 壳子上。

## 实施时的推荐阅读顺序

开始实现 pine-cpp 前，建议结合以下文档一起读：

1. 本文档
2. `llmdoc/architecture/dag-engine.md`
3. `llmdoc/architecture/apple-compiler.md`
4. `llmdoc/guides/cross-layer-validation.md`
5. `llmdoc/reference/operator-contract.md`

pine-cpp 不应成为脱离现有契约的“新项目”；它是对现有 JSON 契约、多运行时 parity 体系和 fixture 财富的延续。