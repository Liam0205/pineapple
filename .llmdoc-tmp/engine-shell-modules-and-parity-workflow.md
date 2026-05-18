# 引擎层与 Shell 层模块分析及跨语言 Parity 测试工作流

## 一、逻辑模块清单

以下按"引擎层"和"Shell 层"分类。不包括算子具体实现（operators/ 目录内容）。

---

### 引擎层

| # | 模块 | 职责 | Go 文件 | Java 文件 | 对等 | cross-validate 覆盖 |
|---|------|------|---------|-----------|------|---------------------|
| 1 | **Config** | JSON 配置解析、SubFlow 展开、skip 归一化 | `internal/config/load.go`, `types.go` | `Config.java` | ✅ | 隐式（execution parity） |
| 2 | **DAG Builder** | 四阶段图构建：屏障→冒险→sources→传递归约 | `internal/dag/dag.go` | `DAG.java` | ✅ | 直接（render-DAG parity） |
| 3 | **DAG Visualizer** | DOT/Mermaid 渲染、SubFlow 折叠 | `internal/dag/visualize.go` | `DAGVisualizer.java` | ✅ | 直接（render-DAG parity） |
| 4 | **Scheduler** | 并发执行计划：等待前驱、skip 评估、ApplyOutput、trace/stats | `internal/runtime/scheduler.go` | `Engine.java`（内联） | ✅ | 隐式（execution parity） |
| 5 | **ParallelExecutor** | Transform 数据分片并行 | `internal/runtime/parallel.go` | `ParallelExecutor.java` | ✅ | fixture 覆盖 |
| 6 | **DataFrame** | 请求本地可变存储：行存/列存、BuildInput/ApplyOutput/ToResult | `internal/dataframe/frame.go`, `row_frame.go`, `column_frame.go` | `Frame.java`, `DataFrame.java`, `ColumnFrame.java` | ✅ | 隐式（execution parity） |
| 7 | **Registry** | 算子 Schema 注册、参数校验+提取、BuildOperator | `internal/registry/registry.go`, `export.go` | `Registry.java` | ✅ | 直接（schema parity） |
| 8 | **Operator Build & Injection** | 构建实例后按 Metadata→Debug→Metrics→Resource 注入 | `pine.go`（NewEngine） | `Engine.java`（createInternal） | ✅ | 隐式 |
| 9 | **Type System** | 6 种算子类型枚举、输出校验、接口定义 | `internal/types/operator.go` | `OperatorType.java`, `Operator.java`, 各 Aware 接口 | ✅ | 隐式 |
| 10 | **Operator I/O** | OperatorInput（只读快照）、OperatorOutput（写收集器） | `internal/types/operator_io.go` | `OperatorInput.java`, `OperatorOutput.java` | ✅ | 隐式 |
| 11 | **Wire Types** | Request、Result、OpTrace | `internal/types/request.go`, `trace.go` | `Engine.java`（Result）, `OpTrace.java` | ✅ | 隐式 |
| 12 | **Errors** | 5-6 种结构化错误（ConfigError、ValidationError 等） | `internal/types/errors.go` | `PineErrors.java` | ✅ | ❌ 未覆盖 |
| 13 | **ResourceManager** | 后台刷新、原子读取、FetcherFactory 注册、ValidateDeps | `pkg/resource/resource.go`, `registry.go`, `validate.go` | `ResourceManager.java`, `ResourceProvider.java`, `ResourceRegistry.java` | ✅ | ⚠️ CLI 模式被 skip |
| 14 | **Metrics/Stats** | 双通道观测：原子统计 + 可插拔 Provider + NopProvider | `internal/runtime/stats.go`, `engine_metrics.go`, `pkg/metrics/` | `Stats.java`, `EngineMetrics.java`, `metrics/Provider.java` 等 | ✅ | ❌ 未覆盖 |
| 15 | **CancellationToken** | 请求级/shard 级取消传播 | 标准库 `context.Context` | `CancellationToken.java` | ✅ | ❌ 无专门 fixture |
| 16 | **Engine Pipeline** | 编排以上模块完成 JSON→Engine 编译 | `pine.go`（NewEngine） | `Engine.java`（create） | ✅ | 隐式（全部 3 步） |
| 17 | **GoFormat** | Java 复制 Go 数值格式化行为 | 无（直接 `fmt`/`strconv`） | `GoFormat.java` | Java 特有 | execution parity 验证 |

---

### Shell 层

| # | 模块 | 职责 | Go 文件 | Java 文件 | 对等 | cross-validate 覆盖 |
|---|------|------|---------|-----------|------|---------------------|
| 18 | **HTTP Server** | 长服务：4 端点、热加载、snapshot、middleware、安全加固 | `pkg/server/server.go`, `http_metrics.go`, `cmd/pineapple-server/` | `PineServer.java` | ✅ | ❌ 不在 cross-validate |
| 19 | **One-shot CLI** | 加载 config+request → 执行 → 输出 result JSON | `cmd/pineapple-run/main.go` | `RunCli.java` | ✅ | 直接（execution parity） |
| 20 | **DAG Render CLI** | 加载 config → 渲染 DAG → 输出 DOT/Mermaid | `cmd/pineapple-dag/main.go` | `RenderDAGCli.java` | ✅ | 直接（render-DAG parity） |
| 21 | **Codegen CLI** | Registry 导出 Schema、生成 Python bindings + 文档 | `cmd/pineapple-codegen/`, `pkg/codegen/` | `Codegen.java`, `ResourceRegistry.java` | ✅ | 直接（schema parity） |

---

## 二、当前跨语言测试覆盖漏洞

### 第一轮（已修复）

| 模块 | 漏洞描述 | 严重度 | 状态 |
|------|----------|--------|------|
| **Errors** | 错误路径无跨语言 fixture（仅 Java 单元测试覆盖） | 中 | ✅ 已修复 (5 error fixtures) |
| **ResourceManager** | `resource_operators.json` 在 CLI 模式被 skip（无 ResourceProvider 注入） | 中 | ✅ 已修复 (--static-resources flag) |
| **Metrics/Stats** | metric 名称/标签一致性无自动化断言 | 低 | ✅ 手动验证一致 |
| **CancellationToken** | 无 timeout/cancel 专门 fixture | 低 | ✅ 已修复 (timeout + Lua error parity) |
| **HTTP Server** | Server 端点行为（错误响应格式、HTTP status code）无跨语言验证 | 中 | ✅ 已修复 (6 HTTP endpoint checks) |
| **DataFrame edge cases** | 列存模式（column）在 cross-validate 中未单独验证 | 中 | ✅ 已修复 (24 column-store cases) |
| **DAG Visualizer** | collapse > 0 的折叠输出未在 cross-validate 中对比 | 低 | ✅ 已修复 (36 DAG render checks) |
| **Config error paths** | 非法 JSON、缺失字段等错误响应格式无跨语言对比 | 低 | ✅ 已修复 (error parity step) |

### 第二轮（2026-05-17 分析）

以当前代码为准重新审视后发现的残余漏洞：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **ColumnFrame** | 稀疏 Additions 列扩展策略不同：Go 对所有已有列 append (nil, false)，Java 只更新涉及列。可能导致后续 BuildInput/ToResult 对稀疏添加行为不一致。 | **中** | 是 | ✅ 已验证为 false positive（presence 机制保证一致），新增 `sparse_recall_column.json` 防回归 |
| 2 | **HTTP Server** | `/execute` 500 partial result body 未对比：当执行中途失败（如 Lua error），两侧都返回 500 + partial result，但 cross-validate.sh 未验证此路径的 response body 结构。 | **中** | 是 | ✅ 已修复 (test [10]: 500 body keys + error message parity) |
| 3 | **HTTP Server** | `/execute` 400 error body 结构未验证：test 4 仅验证 status code=400，不验证 body 是否包含 `error` 字段。 | 低 | 是 | ✅ 已修复 (test [8]: 400 body error field check) |
| 4 | **HTTP Server** | warnings 格式输出未测试：当 `fail_on_error=false` 时算子错误降级为 warning，两侧格式化为 `operator "name": message`。未在 cross-validate 中验证。 | 低 | 是 | ✅ 已修复 (test [11]: 用 Redis 不可达地址触发 warning，验证格式前缀一致) |
| 5 | **Registry** | 错误消息措辞不一致：Go `"operator type not registered"` vs Java `"unknown operator type"`；Go 含 `"for operator %q"` 后缀，Java 不含。功能正确但消息不同。 | 低 | 是 | ✅ 已修复（Java 消息文本对齐 Go，fixture 收紧为精确断言） |
| 6 | **HTTP Server** | response trailing newline 不一致：Go `json.Encoder.Encode()` 追加 `\n`，Java `writeValueAsBytes()` 不追加。对 JSON 消费者无影响。 | 低 | 间接规避 | ✅ 已修复（Java sendResponse 追加 `\n`，字节级一致） |
| 7 | **HTTP Server / Stats** | `/stats` 嵌套结构未深度对比：test 6 仅验证 top-level key，不验证 `operators.X` 子结构的字段一致性。 | 低 | 是 | ✅ 已修复 (test [7]: operator stat keys match) |

---

## 三、跨语言 Parity 测试提升工作流

### 设计原则

1. **Fixture 驱动**：所有跨语言验证基于共享 fixture 文件，而非各自硬编码 expected
2. **分层覆盖**：按模块层级逐步补齐，优先级从高到低
3. **增量友好**：新语言后端（如 Rust/C++）只需实现 CLI 接口即可纳入验证
4. **CI 可执行**：所有验证步骤可在 `scripts/cross-validate.sh` 中无人值守运行

### 工作流步骤

```
┌────────────────────────────────────────────────────────────┐
│ Step 1: 识别覆盖漏洞                                        │
│   - 列出上表中的漏洞模块                                     │
│   - 按严重度排序，选取本轮目标                                │
└─────────────────────────┬──────────────────────────────────┘
                          │
                          v
┌────────────────────────────────────────────────────────────┐
│ Step 2: 设计 Fixture 格式                                   │
│   - 每个 fixture 是一个 JSON 文件                            │
│   - 包含: input（config/request/flags）、expected 行为描述    │
│   - 对于 error path: expected 包含 error_type + message 片段 │
│   - 对于 server: expected 包含 HTTP status + body 结构       │
└─────────────────────────┬──────────────────────────────────┘
                          │
                          v
┌────────────────────────────────────────────────────────────┐
│ Step 3: 编写 Fixture                                        │
│   - 放入 fixtures/ 对应子目录                                │
│   - 先在一侧（通常是 Go）验证行为正确性                       │
│   - 记录 expected output 或 error                           │
└─────────────────────────┬──────────────────────────────────┘
                          │
                          v
┌────────────────────────────────────────────────────────────┐
│ Step 4: 扩展验证脚本                                        │
│   - 在 cross-validate.sh 中新增验证步骤                      │
│   - 或新建专用脚本（如 cross-validate-errors.sh）            │
│   - 调用两侧 CLI 获取输出/错误，比对                         │
└─────────────────────────┬──────────────────────────────────┘
                          │
                          v
┌────────────────────────────────────────────────────────────┐
│ Step 5: 对齐另一侧实现                                      │
│   - 若 Java 行为不一致，修复 Java                            │
│   - 若 Go 行为不一致，修复 Go                                │
│   - 每修一个差异，fixture 自动验证                            │
└─────────────────────────┬──────────────────────────────────┘
                          │
                          v
┌────────────────────────────────────────────────────────────┐
│ Step 6: CI 集成                                             │
│   - 确保新验证步骤在 ci.yml cross-validation job 中运行       │
│   - 更新 summary 输出格式                                   │
└────────────────────────────────────────────────────────────┘
```

### Fixture 目录组织建议

```
fixtures/
├── pipelines/              # 已有：happy path 端到端 execution
├── errors/                 # 新增：错误路径 fixture
│   ├── config_missing_operators.json
│   ├── config_cycle.json
│   ├── config_unknown_operator.json
│   └── request_missing_common_field.json
├── server/                 # 新增：HTTP server 行为 fixture
│   ├── execute_400_missing_field.json
│   ├── execute_413_body_too_large.json
│   └── stats_response_structure.json
├── dag/                    # 新增：DAG 渲染 fixture（含 collapse）
│   ├── collapse_level_1.json
│   └── collapse_level_2.json
└── resources/              # 新增：资源相关 fixture（含 static_resources）
    └── static_resource_lookup.json
```

### Fixture 格式规范

#### Execution fixture（已有格式）

```json
{
  "config": { "pipeline_config": {...}, ... },
  "cases": [
    {
      "request": { "common": {...}, "items": [...] },
      "expected": { "common": {...}, "items": [...] }
    }
  ]
}
```

#### Error fixture（新增格式）

```json
{
  "description": "Cycle in DAG via sources",
  "config": { "pipeline_config": {...} },
  "expected_error": {
    "phase": "compile",
    "error_type": "ConfigError",
    "message_contains": "cycle"
  }
}
```

#### DAG render fixture（新增格式）

```json
{
  "config": { "pipeline_config": {...} },
  "format": "dot",
  "collapse": 1,
  "expected_output_file": "collapse_level_1.dot"
}
```

### 验证脚本扩展方案

`cross-validate.sh` 当前结构：

```
[1/3] Schema parity
[2/3] Render-DAG parity
[3/3] Execution parity
```

扩展为：

```
[1/6] Schema parity (codegen 导出对比)
[2/6] Render-DAG parity (全展开 DOT)
[3/6] Render-DAG parity (折叠 DOT)        ← 新增
[4/6] Execution parity (happy path)
[5/6] Execution parity (column store)      ← 新增: storage_mode=column
[6/6] Error parity (错误路径)              ← 新增
```

### 资源 fixture 的特殊处理

`resource_operators.json` 需要 ResourceProvider 注入，CLI 模式无法直接测试。解决方案：

1. 在 `pineapple-run` / `RunCli` 中增加 `--static-resources` flag
2. 接受一个 JSON 文件描述 static resources（key→value 映射）
3. CLI 内部创建 `StaticResourceProvider` 注入引擎
4. 这样 cross-validate.sh 可以测试 resource 路径

### 优先级路线图

| 优先级 | 模块 | 预期 fixture 数 | 价值 | 状态 |
|--------|------|-----------------|------|------|
| P0 | Error parity | 5-8 个 | 确保两引擎对非法输入返回一致的错误分类 | ✅ 已完成 (5 fixtures) |
| P0 | Column store execution | 复用已有 fixture，加 `storage_mode` | 确保行存/列存语义一致 | ✅ 已完成 (24 cases) |
| P1 | DAG collapse parity | 3-5 个 | 确保折叠渲染一致 | ✅ 已完成 (36 render-DAG checks) |
| P1 | Resource fixture | 2-3 个 | 覆盖被 skip 的资源路径 | ✅ 已完成 (CLI --static-resources, 24 exec + 24 col cases) |
| P2 | Server HTTP behavior | 4-6 个 | 确保错误响应格式/status code 一致 | ✅ 已完成 (6 HTTP checks) |
| P2 | CancellationToken | 1-2 个（slow Lua + timeout） | 确保取消传播行为一致 | ✅ 已完成 (2 checks: timeout + Lua error) |
| P3 | Metrics name parity | schema-level 对比 | 确保 metric 注册名称一致 | ✅ 手动验证一致（硬编码值完全匹配） |

#### 第二轮路线图（2026-05-17 新发现）

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P1 | ColumnFrame 稀疏 Additions | Go 对所有列 append，Java 只更新涉及列 | 可能导致稀疏 recall 场景下结果不一致 | ✅ 验证为 false positive，已加防回归 fixture |
| P1 | Server 500 partial result body | 执行失败时 response body 结构未对比 | 确保 partial result 一致 | ✅ 已实现 (test [10]) |
| P2 | Server 400 body 结构 | 仅验证 status code，不验证 body | 确保 error 字段存在 | ✅ 已实现 (test [8]) |
| P2 | Server /stats 嵌套结构 | 仅验证 top-level keys | 确保 operators 子结构一致 | ✅ 已实现 (test [7]) |
| P3 | Registry 错误消息措辞 | Go/Java 消息文本不同 | 无功能影响，已通过子串匹配规避 | ✅ 已对齐（Java 消息文本对齐 Go） |
| P3 | Server trailing newline | Go 追加 `\n`，Java 不追加 | 无功能影响，JSON 解析规避 | ✅ 已修复（Java 追加 `\n`） |
| P3 | Server warnings 格式 | 无触发 warning 的 fixture | 需 fail_on_error=false + 外部服务 mock | ✅ 已修复（用 Redis 不可达地址触发，test [11]） |

### 第三轮（2026-05-17 深度分析）

以全量代码审计为准，发现的新覆盖漏洞：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **DAG Visualizer** | Mermaid `classDef` 行顺序不确定：Go 遍历 map（无序），Java 遍历 enum（固定序）。字节级比对必定失败。 | **中** | 是（需先修 Go 排序） | ✅ 已修复（Go 用 AllOperatorTypes 有序切片，加 mermaid parity test，37→74 checks） |
| 2 | **Codegen** | Schema parity 仅比较 operator names + param types/required，**不比较 Default 值**。Default 差异可能导致运行时行为不一致。 | **中** | 是 | ✅ 已修复（扩展 Python 对比含 Default 字段，带数值归一化） |
| 3 | **Registry** | JSON 数值类型差异：Go `json.Unmarshal` 将所有 JSON number 解析为 `float64`；Jackson 将整数解析为 `Integer`/`Long`。如果算子做 strict type assert 会静默失败。 | **中** | 是 | ✅ 验证为 false positive（7 个 fixture 已隐式覆盖，双侧算子均做灵活类型转换） |
| 4 | **HTTP Server** | `_return_trace=true` 响应结构完全未测试：trace 字段名、duration_ms 精度、skipped 字段存在性均无对比。 | 低 | 是 | ✅ 已修复 (test [10]: trace count + keys 结构比对) |
| 5 | **Config / Errors** | 缺失 4-5 个错误路径 fixture：DAG cycle、forward source ref、skip 字段无下划线前缀、pipeline_map 引用不存在的 group。 | 低 | 是 | ✅ 已部分修复 (+2 fixtures: forward ref + unknown source，覆盖 7/7) |
| 6 | **ParallelExecutor** | 1 item + `data_parallel>1` 的退化路径未测试：Go 跳过 shard 创建，Java 创建后短路。 | 低 | 是 | ✅ 已修复 (data_parallel.json 新增 1-item case) |
| 7 | **HTTP Server** | 超大请求体 >10MB 的 413 响应未测试：双侧默认 10MB 限制。 | 低 | 是 | ✅ 已修复 (test [13]: 11MB body → 413) |
| 8 | **GoFormat** | `sprint(Map/List)` 格式不同：Go `fmt.Sprintf("%v")` 输出 `map[k:v]`，Java `toString()` 输出 `{k=v}`。实践中 filter_condition 不会用在 map 字段上。 | 低 | 理论上 | ⏸️ 风险极低，仅记录 |

#### 第三轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P0 | DAG Visualizer Mermaid | classDef 行顺序不确定 | 修复后可启用 mermaid parity test | 待实现 |
| P0 | Codegen Default 值 | Default 差异可能导致运行时不一致 | 补齐 schema 对比 | 待实现 |
| P1 | Registry 数值类型 | Jackson int vs Go float64 | 确认所有 int param 行为一致 | ✅ 验证为 false positive（7 个 fixture 已隐式覆盖） |
| P2 | Server _return_trace | trace 结构未验证 | 确保 trace 字段名/结构一致 | ✅ 已实现 (test [10]: count + keys 比对) |
| P2 | Config 错误路径 | 缺少 4-5 个 error fixtures | 扩大错误场景覆盖 | ✅ 已实现 (+2: forward ref + unknown source) |
| P2 | ParallelExecutor 退化路径 | 1-item shard 行为 | 防回归 | ✅ 已实现 (data_parallel 新 case) |
| P3 | Server 413 | 超大 body 处理 | 确保限制行为一致 | ✅ 已实现 (test [13]: 11MB→413) |
| P3 | GoFormat Map/List | 格式化差异 | 实践中不触发 | ⏸️ 仅记录（无实际风险） |

### 第四轮（2026-05-17 全面代码审计）

在第三轮全部完成后，重新审视所有引擎/Shell 层模块的行为路径覆盖：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **DataFrame / Config** | `common_defaults` / `item_defaults` 功能完全未测试：两侧在 BuildInput 中对 nil/missing 字段应用默认值，但无 fixture 验证。若一侧仅对 nil 应用、另一侧对 missing 也应用，结果会静默不同。 | **中** | 是（fixture） | 新建含 defaults 的 pipeline fixture，请求中省略该字段，验证结果包含默认值 |
| 2 | **DAG / Type System** | Observe 类型算子零 fixture 覆盖：`observe_log` 两侧均已实现，但无 pipeline fixture 测试。Observe 有独特 DAG 语义（非阻塞、只读 RAW 依赖），若一侧误当 barrier 处理不会被发现。 | **中** | 是（fixture） | 新建含 observe_log 的 pipeline fixture，验证执行结果正确且不阻塞后续算子 |
| 3 | **ParallelExecutor** | 分片错误传播未测试：当 `data_parallel>1` 且某 shard 执行失败时，Go 用 `PanicError` 包装，Java 用 `OperatorException`/`RuntimeException` 包装。错误消息格式可能不一致。 | **中** | 是（fixture） | 新建 data_parallel fixture，Lua 算子在特定条件下 error()，验证双侧返回一致的错误消息 |
| 4 | **HTTP Server** | `/dag?format=mermaid` HTTP 端点未测试：test [5] 仅测 default（dot）。虽然 CLI 已验证 mermaid parity，但 HTTP 端点的 query param 路由可能不同。 | 低 | 是（curl） | 新增 server test：`/dag?format=mermaid` 对比双侧输出 |
| 5 | **HTTP Server** | `/dag?format=invalid` 错误响应未测试：Go 返回 JSON error via writeJSON，Java 通过 sendResponse(400)。body 结构可能不同。 | 低 | 是（curl） | 新增 server test：`/dag?format=xyz` 对比 status + body |
| 6 | **HTTP Server** | test [9] validation error 仅验证 status code，不验证 body 结构（Go omitempty 可能省略 warnings/trace 字段，Java 可能不省略）。 | 低 | 是（curl） | 扩展 test [9] 对比 response body key 集合 |

#### 第四轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P0 | DataFrame defaults | common_defaults/item_defaults 未测试 | 核心执行路径默认值语义 | ✅ 已修复 (defaults_common_item.json, 4 cases) |
| P0 | Observe fixture | observe_log 零覆盖 | 唯一未测试的算子类型 | ✅ 已修复 (observe_log_readonly.json, 2 cases) |
| P1 | ParallelExecutor error | shard 错误传播 | 并发错误语义一致性 | ✅ 已修复 (runtime_parallel_shard_error.json) |
| P2 | Server /dag mermaid | HTTP 端点 format=mermaid | 端点行为一致性 | ✅ 已修复 (test [12]) |
| P2 | Server /dag invalid | HTTP 端点 format=invalid error | 端点错误一致性 | ✅ 已修复 (test [13]) |
| P2 | Server body keys | test [9] body 结构 | 验证 error response 完整性 | ✅ 已修复 (test [14]) |

### 第五轮（2026-05-17 深度边界 case 分析）

在第四轮完成后，聚焦于组合路径和配置边界 case：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **Scheduler / Skip** | 多元素 skip 数组（OR 语义）未测试：`skip: ["_guard_a", "_guard_b"]`，现有 fixture 仅用字符串 skip。任一 guard 为真时算子应被跳过。 | **中** | 是（fixture） | 新建 fixture，单算子有多个 skip guard，分别测试只有一个/两个/零个为真的场景 |
| 2 | **Config** | 非 "main" 单入口 pipeline_group 未测试：`pipeline_group: {"custom": {...}}`（无 "main" key）应通过 size==1 回退逻辑正常执行。 | **中** | 是（fixture） | 新建 fixture 用非 "main" key 的 pipeline_group |
| 3 | **HTTP Server** | `/dag?collapse=N` HTTP 端点未测试：CLI 已验证折叠 parity，但 HTTP 端点未覆盖。 | **中** | 是（curl） | 在 server 测试中加入 `/dag?collapse=1` 对比 |
| 4 | **Engine / renderDAG** | Java 用 `equalsIgnoreCase` 匹配格式，Go 用精确匹配。`format=DOT` 在 Java 成功、Go 失败。实际代码分歧。 | **中** | 是 | 修复 Java 为精确匹配对齐 Go |
| 5 | **Config / Error** | 多 pipeline_group 无 "main" 的错误路径无 fixture：双侧有相同逻辑但无验证。 | 低 | 是（error fixture） | 新增 error fixture |
| 6 | **Engine / Contract** | `items: []` + 声明 `item_input` 的边界 case：双侧跳过验证（by design），但无 fixture 显式验证空 items 返回空 items。 | 低 | 是（fixture case） | 在现有 fixture 中加 items=[] case |

#### 第五轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P0 | Skip 多元素数组 | OR 语义 | 核心调度语义 | ✅ 已修复 (skip_multi_guard.json, 4 cases) |
| P0 | renderDAG case sensitivity | Java equalsIgnoreCase 分歧 | 修复代码分歧 | ✅ 已修复 (Engine.java equals→equalsIgnoreCase) |
| P1 | 非 "main" pipeline_group | 回退逻辑 | 配置路径覆盖 | ✅ 已修复 (non_main_pipeline_group.json, 2 cases) |
| P1 | HTTP /dag?collapse | HTTP 端点折叠 | 端点行为一致 | ✅ 已修复 (test [12b]) |
| P2 | Error: 多 group 无 main | 错误路径 | 扩大错误覆盖 | ✅ 已修复 (config_multi_group_no_main.json) |
| P2 | Empty items 边界 | 空 items 语义 | 边界 case 防回归 | ✅ 已修复 (non_main_pipeline_group case 2: items=[]) |

### 第六轮（2026-05-17 JSON 序列化精确性分析）

在第五轮完成后，聚焦于跨语言 JSON 数值序列化一致性：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **TransformByLua / JSON** | Lua 整数返回值序列化不一致：Go `json.Marshal(float64(6.0))` → `6`，Java Jackson `Double(6.0)` → `6.0`。`normalize_json` 掩盖了此差异，但裸 JSON 消费者会看到不同输出。 | **中** | 是（代码修复） | Java `toJava()` 对整数 double 转换为 Long |

#### 第六轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P0 | TransformByLua toJava() | 整数 Double→Long | JSON 输出字节级一致 | ✅ 已修复（toJava 返回 long，fixture expected 同步更新） |

### 第七轮（2026-05-17 微缺口收尾）

在第六轮完成后，全面代码审计确认无中/高严重度差异。仅修复 3 项极低严重度风格/完整性缺口：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **HTTP Server** | Go `/health` 缺少 trailing newline：`handleHealth` 用 raw Write 绕过了 `writeJSON`，不追加 `\n`。Java 通过 `sendResponse` 统一追加。 | 低 | 是 | ✅ Go 改用 writeJSON |
| 2 | **cross-validate** | 不验证 response Content-Type headers：所有 17 个 HTTP test 仅验证 status code + body，header 差异不可捕获。 | 低 | 是 | ✅ 新增 test [15] |
| 3 | **Config / SubFlow** | depth-3+ SubFlow 嵌套无 fixture：逻辑等价已确认但无回归防护。 | 低 | 是 | ✅ 新增 deeply_nested_subflow.json (2 cases) |

#### 第七轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P3 | Server /health newline | trailing newline 不统一 | 字节级一致 | ✅ 已修复 (writeJSON) |
| P3 | cross-validate headers | 无 Content-Type 验证 | 防 header 偏移 | ✅ 已修复 (test [15]) |
| P3 | SubFlow depth-3 | 无回归防护 | 深嵌套展开防回归 | ✅ 已修复 (2 cases) |

---

### 总结

经过 7 轮系统化分析和修复，引擎层和 Shell 层所有已知跨语言 parity 差异均已覆盖：

| 指标 | 值 |
|------|-----|
| Execution parity cases | 43 (row) + 43 (column) |
| DAG render checks | 90 |
| Error parity fixtures | 15 |
| Server HTTP checks | 21 |
| Cancellation checks | 2 |
| Schema parity | 完整（含 defaults + Python byte-level） |

### 对新语言后端的扩展性

当引入新后端（如 Rust/C++）时：

1. 实现三个 CLI 工具：`run-cli`、`render-dag-cli`、`codegen-cli`
2. CLI 接口遵循已有 flag 约定（`-config`、`-request`、`-format`、`-collapse`）
3. `cross-validate.sh` 只需新增一个 `backend_run()` 函数即可纳入
4. 共享 fixture 无需修改——新后端的正确性由"与已有后端输出一致"保证

这个设计确保 parity 测试是**后端数量的线性增长**（N 个后端 = N-1 次两两比对），而不是 N² 的全排列。

### 第八轮（2026-05-18 引擎重构后深度审计）

在完成 Java 最佳实践重构（13 commits）后，基于当前代码重新审计引擎/Shell 层行为路径覆盖：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **Config/SubFlow** | SubFlow 循环引用（`pipeline_map` 中的环）错误路径无 fixture：双侧都实现了 visiting set 环检测但消息格式未验证。 | **高** | 是 | 新增 `config_subflow_cycle.json` |
| 2 | **Config/SubFlow** | operator 名与 pipeline_map 键冲突错误路径无 fixture：Go 两次检测（validate + expand）可能消息不同。 | **高** | 是 | 新增 `config_name_collision_operator_subflow.json` |
| 3 | **DataFrame** | 单个算子同时发出 Removal + Reorder 的行为无 fixture：当前 filter 和 reorder 总是在不同算子中。 | **高** | 是 | 新增带 Lua merge 的 fixture 验证 remove+reorder 组合 |
| 4 | **Engine/Config** | `data_parallel` 三个编译期拒绝路径（负值、非 Transform、未实现 ConcurrentSafe）无 error fixture。 | **中** | 是 | 新增三个 error fixtures |
| 5 | **DataFrame** | 全部 items 被 filter 移除（空帧 ToResult）无显式 case。 | **中** | 是 | 在已有 fixture 添加一个全移除 case |
| 6 | **Engine/数值** | NaN/Infinity 穿透到 JSON 序列化时双侧行为未验证。Go→PanicError, Java→JsonGenerationException。 | **中** | 是 | 新增 server 级 fixture 或 error fixture |
| 7 | **ParallelExecutor** | data_parallel > 1 + items=[] 时的退化路径未显式验证。 | **中** | 是 | data_parallel fixture 添加空 items case |
| 8 | **Config** | `for_branch_control` 保留键设置后不影响执行的行为无验证。 | 低 | 是 | 已有 fixture 加 `"for_branch_control": true` |
| 9 | **Config** | operator 在 pipeline 中重复引用的错误路径无 fixture。 | 低 | 是 | 新增 error fixture |
| 10 | **Config** | `storage_mode` 非法值回退 row 的行为无 fixture。 | 低 | 是 | 新增 fixture |

#### 第八轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P0 | Config SubFlow cycle | 环检测消息 parity | 核心错误路径 | ✅ 已修复 (config_subflow_cycle.json) |
| P0 | Config name collision | operator/subflow 冲突 | 核心错误路径 | ✅ 已修复 (Go 消息对齐 Java + fixture) |
| P0 | DataFrame removal+reorder | 同算子组合输出 | 核心执行路径 | ⏸️ 无内建算子可触发，已验证 applyOutput 逻辑一致 |
| P1 | data_parallel 编译期拒绝 | 三个校验路径 | 编译期安全 | ✅ 已修复 (2 fixtures: non-Transform + not ConcurrentSafe) |
| P1 | 空帧 filter | ToResult 空 items | 边界执行路径 | ✅ 已修复 (filter_removes_all.json) |
| P1 | NaN/Infinity 序列化 | 错误行为 parity | 运行时安全 | ✅ 已修复 (validateValue 拒绝 NaN/Inf + fixture) |
| P2 | data_parallel 空 items | 退化路径 | 边界 case | ✅ 已修复 (data_parallel.json case 4) |
| P2 | for_branch_control | 无副作用验证 | 配置健壮性 | ✅ 已修复 (skip_branch.json 加标记) |
| P3 | 重复 operator ref | 错误消息 | 错误路径覆盖 | ✅ 已修复 (config_duplicate_operator_ref.json) |
| P3 | 非法 storage_mode | 回退行为 | 边界 case | ⏸️ 隐式覆盖（execution parity 保证 fallback 一致）|

### 第九轮（2026-05-18 全新深度审计）

在第八轮完成后（含 P3 3.1 Java validateValue field name 修复），对全部引擎/Shell 层代码做 fresh audit：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **HTTP Server `/health`** | Go `handleHealth` **无 HTTP method 检查**：接受任意方法（POST/PUT/DELETE→200）。Java 明确拒绝非 GET 返回 405。 | **中** | 是（curl） | Go 添加 `if r.Method != http.MethodGet` 守卫 |
| 2 | **HTTP Server `/execute`** | 请求 JSON 无 "common" key 时行为分歧：Go 得到 nil→ValidationError `"request.Common must not be nil"`；Java 用 `getOrDefault(emptyMap)` 绕过 nil check→不同错误消息。 | 低 | 是（curl） | Java 改用 `req.get("common")` 保持 nil 传播 |
| 3 | **RunCli 输出** | Go `json.Encoder.Encode()` 追加 `\n`，Java `System.out.print(json)` 不追加。Shell `$()` 截断 trailing newline 导致 cross-validate 掩盖差异。 | 低 | 间接规避 | Java 改为 `println` |

#### 第九轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P1 | Server /health method | 405 vs 200 分歧 | 端点行为一致 | ✅ 已修复 (Go 添加 method guard + test [15b]) |
| P2 | Server nil common | 错误消息分歧 | 错误路径一致 | ✅ 已修复 (Java `req.get` + test [15c]) |
| P3 | RunCli trailing newline | 字节级输出一致 | 防潜在对比失败 | ✅ 已修复 (Java println) |

### 第十轮（2026-05-18 邻近层扩展审计）

将审计范围扩展到引擎/Shell 层邻近区域：资源管理、Metrics 发射、Codegen 输出、JSON 序列化、Engine 生命周期：

| # | 模块 | 漏洞描述 | 严重度 | 可验证 | 建议 |
|---|------|----------|--------|--------|------|
| 1 | **Stats JSON key ordering** | Go `encoding/json` 对 map 键字母排序；Java `ConcurrentHashMap` 迭代顺序不确定。`/stats` 响应中 operator 名顺序不同。 | **中** | 是（curl） | Java `snapshot()` 改用 `TreeMap` |
| 2 | **Codegen whitespace** | Go 模板 vs Java PrintWriter 可能产生不同空白行。 | 中（待验证） | 是 | 实际验证为 false positive：生成输出字节级一致 |
| 3 | **Resource Fetcher context** | Go `Fetcher` 接收 `context.Context`（可取消）；Java 无 context 参数。 | 低 | 否（内部） | 接受的平台差异（仅影响 shutdown 时序） |
| 4 | **Resource Stop timeout** | Go `wg.Wait()` 无限等待；Java `awaitTermination(5s)`。 | 低 | 否（内部） | 接受的平台差异 |
| 5 | **Codegen string escaping** | Go `%q` vs Java `escapeString()` 对异国字符可能不同。 | 低 | 理论上 | 当前无异国默认值，无实际风险 |

#### 第十轮路线图

| 优先级 | 模块 | 漏洞 | 价值 | 状态 |
|--------|------|------|------|------|
| P1 | Stats key ordering | operator 名字母排序 | API 消费者可见 | ✅ 已修复 (Java TreeMap + test [7b]) |
| P2 | Codegen Python output | 字节级对比 | CI 可验证 | ✅ 验证为一致 + 加字节对比测试 |
| P3 | Resource context/timeout | 内部差异 | 无消费者影响 | ⏸️ 接受的平台差异 |
| P3 | Codegen string escaping | 异国字符默认值 | 当前无实际触发 | ⏸️ 仅记录 |
