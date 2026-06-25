[English](README-en.md) | 简体中文

# Pineapple

高性能 DAG 流水线引擎。**Python 声明，Go/Java/C++ 三引擎执行，JSON 解耦。**

算子只需声明输入/输出字段，引擎自动推导依赖、构建 DAG、并行调度——你专注业务逻辑，Pineapple 负责把它跑快。

适用于任何需要**多步骤数据处理流水线**的场景：搜索/推荐/广告排序、特征工程、实时数据加工、规则引擎、ML 推理前后处理，等等。

> **⚠️ Pre-1.0 阶段**：API 和行为语义可能在版本间发生不兼容变更。生产环境使用请锁定具体版本。

## 架构

```
Python DSL (Apple)  ──compile──>  JSON Config
                                      │
                          ┌───────────┼───────────┐
                          v           v           v
                   Pine-Go (Go)  Pine-Java     Pine-C++
                   构建 DAG       构建 DAG      构建 DAG
                   并行执行       并行执行      per-node 并行
```

| 组件 | 语言 | 职责 |
|------|------|------|
| **Apple** | Python | 声明式 DSL，编译输出 JSON 配置 |
| **Pine-Go** | Go | 主执行引擎：解析配置、构建 DAG、并行调度 |
| **Pine-Java** | Java | 第二执行引擎，与 Pine-Go 行为一致 |
| **Pine-C++** | C++23 | 第三执行引擎（标杆运行时），完全 parity + 性能上限探索 |

**工程团队**用 Go/Java/C++ 开发高性能算子；**业务团队**用 Python DSL 编排逻辑。两侧通过 JSON 配置彻底解耦。

> 曾经存在的 Pine-Python 运行时引擎已于 v0.9.7 后移除。仓库中的 Python 代码仅为 Apple DSL 声明层（编译器），不是运行时。

## 核心特性

- **隐式构图** — 算子声明输入/输出字段，引擎自动推导 DAG 依赖并执行传递性归约
- **无锁并行** — DAG 中无依赖的算子自动并行执行
- **编译期校验** — 死代码、字段缺失、写后未读等问题在部署前拦截
- **Lua 嵌入** — 内置 Lua 算子支持轻量自定义计算。pine-go 默认 [wangshu](https://github.com/Liam0205/wangshu)（纯 Go Lua 5.1 VM，NaN-boxing + arena GC），可通过 `-tags=lua_gopher` 切回 gopher-lua；pine-java 用 LuaJC（字节码编译），pine-cpp 用 LuaJIT。端到端开销约 1.2-2x；隔离算子级开销随运行时与计算复杂度变化（C++/LuaJIT 约 3-5x、Java 约 2-9x、Go 约 6-17x），计算密集型热路径建议写原生算子
- **配置热加载** — 服务运行时自动无停机重载引擎配置
- **动态资源** — 双通道资源管理：**数据型**（如静态 dict / 实时 feature store，snapshot 导出后无锁读）+ **句柄型**（如 `redis_connection`，borrow 借用 + RAII 拆除）；后台定时刷新
- **Redis cascade-safety** — `redis_connection` 资源暴露 `{dial,read,write,pool}_timeout_ms` + `pool_size` 五参数，per-command 指标 `pine_redis_command_*`（4-state status：ok / timeout / pool_timeout / error），fail-on-error 静默降级契约
- **白盒可观测** — 算子级 trace；`/stats` 组合响应含 `/stats.http`（请求级 4-state 指标）+ `/stats.resources`（资源池连接池/探针/per-command 4 状态分类）；可插拔 Prometheus 接口
- **行存/列存可切换** — DataFrame 支持两种存储模式
- **三引擎一致性** — Go/Java/C++ 引擎通过 CI 交叉验证保证 schema、DAG、执行结果、错误消息字节级一致（19 section cross-validate + 三引擎差分 fuzz + 每日 ASan/TSan sanitized fuzz）
- **Pine-C++ 标杆运行时** — 完整第三运行时，内置算子与 Go/Java 完全对等、HTTP server（热加载/graceful shutdown）、ColumnFrame/RowFrame 双物理实现、OperatorInput lazy 投影、LuaJIT 集成、metrics/resource 对等

## Quick Start

### 环境要求

- Go 1.26+（Pine-Go）
- Java 21+（Pine-Java）
- Python 3.11+（Apple DSL）
- CMake 3.20+ / C++23 编译器 / LuaJIT（Pine-C++，可选）

### 1. 编写 Pipeline

```python
from apple.flow import Flow

flow = Flow(
    name="demo",
    common_input=["user_age"],
    item_output=["item_id", "item_final_price"],
)

flow.recall_static(
    item_output=["item_id", "item_price"],
    items=[
        {"item_id": "a", "item_price": 100.0},
        {"item_id": "b", "item_price": 200.0},
    ],
)

flow.transform_by_lua(
    common_input=["user_age"],
    item_input=["item_price"],
    item_output=["item_final_price"],
    lua_script="""
function discount()
  if user_age < 18 then return item_price * 0.8
  else return item_price end
end
""",
    function_for_item="discount",
)

flow.reorder_sort(
    item_input=["item_final_price"],
    field="item_final_price",
    order="desc",
)

with open("pipeline.json", "w") as f:
    f.write(flow.compile())
```

### 2. 启动服务

```bash
go run ./pine-go/cmd/pineapple-server -config pipeline.json -addr :8080
```

### 3. 发送请求

```bash
curl -s -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"common": {"user_age": 16}, "items": []}' | python3 -m json.tool
```

修改 Python 后重新编译，服务自动热加载，无需重启。

## 项目结构

```
pineapple/
├── apple/                  # Python DSL (Apple)
│   ├── flow.py             #   Flow/SubFlow 声明
│   ├── compiler.py         #   编译器：DSL → JSON
│   ├── validator.py        #   静态校验器
│   └── tests/              #   Python 测试
├── apple_generated/        # codegen 自动生成的 Python 绑定
├── pine-go/                # Go 执行引擎 (Pine-Go)
│   ├── cmd/                #   CLI 工具
│   │   ├── pineapple-server/   # HTTP 服务
│   │   ├── pineapple-codegen/  # 代码 & 文档生成
│   │   ├── pineapple-dag/      # DAG 渲染
│   │   └── pineapple-run/      # 单次执行
│   ├── internal/           #   内部包（config/dag/dataframe/runtime）
│   ├── operators/          #   内置算子
│   ├── pkg/                #   可复用库（server/codegen/metrics/resource）
│   ├── integration/        #   集成测试
│   └── benchmarks/         #   性能基准测试
├── pine-java/              # Java 执行引擎 (Pine-Java)
│   ├── src/main/java/      #   引擎实现 + CLI 工具
│   └── src/test/java/      #   测试 + 基准 + fuzz
├── pine-cpp/               # C++ 执行引擎 (Pine-C++)
│   ├── include/pine/       #   公共头文件
│   ├── src/                #   config/dag/dataframe/runtime/server/lua/redis/http/resource
│   ├── operators/          #   内置算子（与 Go/Java 对等）+ bench stubs（编译开关 PINE_BUILD_BENCH_STUBS）
│   ├── cmd/                #   pineapple-run / pineapple-render-dag / pineapple-server / pineapple-codegen / pineapple-cause-chain-probe
│   └── tests/              #   doctest 单测套件
├── fixtures/               # 共享测试 fixtures（三引擎公用）
│   ├── operators/          #   算子级单元 fixtures
│   ├── pipelines/          #   Pipeline 级端到端 fixtures
│   ├── errors/             #   错误路径 fixtures
│   ├── error_chain/        #   ExecutionError 因果链 fixtures
│   ├── server_byte_exact/  #   server 响应字节级一致 fixtures
│   └── benchmarks/         #   benchmark 配置/请求（含 calibrated 生产 proxy）
├── scripts/                # 开发者脚本
├── design_doc/             # 设计文档
└── doc/                    # 生成的算子文档 & 报告
```

### Pine-C++ 标杆运行时

`pine-cpp/` 是完整的第三运行时（C++23），定位为**在完全 parity 前提下的标杆实现**。

当前能力：

- **内置算子与 Go/Java 完全对等**（清单见 `pine-cpp/CMakeLists.txt` 与 `doc/operators/`）
- **HTTP server**：`pineapple-server`，含热加载、graceful shutdown、HTTP/1.1 keep-alive、客户端断连取消、`/health`/`/execute`/`/stats`/`/dag` 端点
- **CLI**：`pineapple-run`、`pineapple-render-dag`、`pineapple-codegen`、`pineapple-cause-chain-probe`
- **Frame 多态**：ColumnFrame（列存）+ RowFrame（行存），OperatorInput lazy 投影；锁形态（per-call `shared_mutex`）与 Go/Java 完全镜像
- **LuaJIT**：StatePool、沙箱隔离、`_G["..."]` 变量注入
- **可观测**：`metrics::Provider`、`resource::Manager`、`/stats.http`、`/stats.resources`、cause chain
- **CI**：cpp-build、cpp-test（doctest）、cpp-sanitizer（ASan/UBSan）、cpp-tsan（ThreadSanitizer）、cpp-lint（-Werror）
- **Cross-validate**：全 section 接入，三引擎一致性验证


### 常用脚本

| 脚本 | 用途 |
|------|------|
| `scripts/go-test.sh` | Go 全量测试 |
| `scripts/java-test.sh` | Java 全量测试 |
| `scripts/test-all.sh` | Go + Apple(Python) + Java 全量测试 |
| `scripts/lint.sh` | Go + Java + Python lint |
| `scripts/go-bench.sh` | Go 性能基准 |
| `scripts/java-bench.sh` | Java 性能基准 |
| `scripts/bench-cross-runtime.sh` | 跨引擎 HTTP server benchmark（fixture 驱动，cgroup 资源隔离） |
| `scripts/go-fuzz.sh` | Go fuzz 测试 |
| `scripts/java-fuzz.sh` | Java fuzz 测试 |
| `scripts/differential-fuzz.sh` | 三引擎差异模糊测试（随机生成 pipeline 比对输出） |
| `scripts/cross-validate.sh` | 三引擎交叉验证（schema + DAG + 执行 + 错误 + server + metrics 等） |
| `scripts/cpp-sanitizer-smoke.sh` | C++ ASan/UBSan 冒烟 |
| `scripts/cpp-tsan-smoke.sh` | C++ ThreadSanitizer 高并发压测 |
| `scripts/codegen.sh` | 代码生成（`--backend go\|java`） |
| `scripts/render-dag.sh` | DAG 可视化（`--backend go\|java`） |
| `scripts/apple-compile.sh` | Apple DSL 编译为 JSON |
| `scripts/run-pipeline.sh` | 单次执行 pipeline |
| `scripts/bump-version.sh` | 版本号同步更新 |

### CI 流水线

CI 在每次 push/PR 时自动运行：

- **Lint** — Go (golangci-lint)、Java (checkstyle, failOnViolation=true)、Python (ruff)、C++ (-Werror)
- **Test** — Go/Java/Apple/C++ 全量测试 + 覆盖率
- **Sanitizer** — C++ ASan/UBSan 冒烟 + ThreadSanitizer 高并发压测
- **Fuzz** — Go/Java fuzz + 三引擎差异模糊测试
- **Benchmark** — Go/Java 性能基准
- **Cross-validation** — 三引擎 schema/DAG/执行/错误/server/metrics 一致性
- **Codegen check** — 确保生成代码与源码同步

### 交叉验证

`scripts/cross-validate.sh` 验证三引擎的一致性，当前 19 个 section（具体以 `scripts/cross-validate/` 目录为准）：

1. **Schema parity** — 三端 codegen 导出的算子 schema 与 apple_generated 产物字节级一致
2. **DAG parity** — 相同配置，三端渲染的 DAG（DOT + Mermaid，含 collapse）必须一致
3. **Execution parity** — 相同配置 + 请求，三端执行结果必须一致
4. **Column-store parity** — 以列存模式重复执行验证
5. **Error parity** — 非法配置/请求，三端返回相同的错误分类和消息
6. **Server parity** — HTTP 端点的 status code、body 结构、Content-Type 一致
7. **Cancellation parity** — 超时、运行时错误与客户端断连的取消行为一致
8. **Concurrent parity** — 并发请求下的行为与计数一致
9. **Raw-byte parity** — 不归一化的原始字节输出比对（key 顺序）
10. **Hot-reload parity** — 配置与 `resource_config` 热加载行为一致
11. **Redis integration** — redis 算子在三端的真实 redis 行为一致
12. **Extensibility parity** — 下游扩展模式（middleware、未注册路径等负空间）一致
13. **Metrics parity** — `/stats` 结构和数值一致（含 lua_pool 计数器、data_parallel 并发不变量）
14. **Byte-exact execute** — server `/execute` 响应字节级一致
15. **Error cause chain** — ExecutionError 因果链可解包一致
16. **Resource metrics** — `/stats.resources` 子树结构与三态（无流量/有量/不可达）一致
17. **Templated params** — `{{field}}` 模板参数解析一致
18. **SubFlow contract stderr** — Apple 编译期 SubFlow 契约报错文案稳定
19. **Bench-stub parity** — bench 构建下 `reorder_topn_boost` 字节级一致

### 为下游构建 Cross-Validation 体系

如果你在 Go 和 Java 中同时实现了自定义算子并需要保证跨语言一致性，可以复用 Pineapple 的 parity 校验框架。

#### 设计原则

1. **Fixture 驱动** — 所有验证基于共享 JSON fixture 文件，而非各语言硬编码 expected 值
2. **CLI 接口统一** — 每个引擎提供相同的 CLI 工具（`-config`、`-request`），输出 JSON 结果
3. **JSON 归一化比对** — 通过 `sort_keys` + 数值类型统一消除平台差异（Go map 无序、float64/Double 表示差异）
4. **增量友好** — 新引擎只需实现 CLI 接口即可纳入验证

#### Fixture 格式

**算子级 fixture**（单算子行为验证）：

```json
{
  "operator": "your_operator_name",
  "cases": [
    {
      "name": "描述性测试名",
      "params": { "param1": "value" },
      "metadata": {
        "common_input": [], "common_output": [],
        "item_input": ["field"], "item_output": ["result"]
      },
      "input": { "common": {}, "items": [{"field": 1}] },
      "expected": { "items": [{"result": 2}] }
    }
  ]
}
```

**Pipeline 级 fixture**（端到端执行验证）：

```json
{
  "name": "fixture 描述",
  "config": { "pipeline_config": {...}, "pipeline_group": {...}, "flow_contract": {...} },
  "cases": [
    {
      "name": "case 描述",
      "request": { "common": {...}, "items": [...] },
      "expected": { "common": {...}, "items": [...] }
    }
  ]
}
```

**错误路径 fixture**：

```json
{
  "name": "error 描述",
  "config": { ... },
  "expected_error": { "type": "ConfigError", "message_contains": "关键词" }
}
```

#### JSON 归一化策略

比对两端输出时，必须消除以下平台固有差异：

```python
def normalize_json(text):
    """Go map 顺序不确定，数值类型表示不同"""
    import json
    obj = json.loads(text)
    # 递归将所有 int 统一为 float（消除 Go int vs Java Double）
    def unify(v):
        if isinstance(v, int): return float(v)
        if isinstance(v, list): return [unify(x) for x in v]
        if isinstance(v, dict): return {k: unify(x) for k, x in v.items()}
        return v
    return json.dumps(unify(obj), sort_keys=True)
```

#### 下游接入步骤

1. 在两侧各实现算子，保证参数名和 `$metadata` 声明一致
2. 创建 fixture 文件，放入共享目录
3. 编写验证脚本：分别调用两端 CLI，归一化输出后逐字节比对
4. 纳入 CI：失败即阻断合并

参考 `scripts/cross-validate.sh` 的完整实现了解实战细节。

## Benchmark

跨引擎性能对比（HTTP server 模式，`scripts/bench-cross-runtime.sh`，10000 请求 × 16 并发，server 以 2C/4G cgroup 隔离）。`realistic_calibrated` 为按真实流量校准的生产 proxy fixture，其余为合成压测。

### 吞吐量 (QPS)

| Fixture | Go | Java | C++ |
|---|---|---|---|
| small_010 (10 items) | 37078 | 5825 | 20794 |
| small_050 (50 items) | 26976 | 5201 | 17244 |
| small_100 (100 items) | 19585 | 4748 | 13904 |
| medium_0100 (100 items) | 12025 | 3681 | 8578 |
| medium_0500 (500 items) | 2921 | 2034 | 2938 |
| medium_1000 (1000 items) | 1446 | 1360 | 1647 |
| large_0100 (100 items) | 6395 | 2855 | 4855 |
| large_0500 (500 items) | 1439 | 1439 | 1671 |
| large_1000 (1000 items) | 728 | 917 | 902 |
| large_5000 (5000 items) | 142 | 212 | 174 |
| **realistic_calibrated (生产校准)** | **120** | **124** | **221** |

### P50 延迟 (ms)

| Fixture | Go | Java | C++ |
|---|---|---|---|
| small_010 | 0.3 | 2.0 | 0.6 |
| medium_0500 | 5.0 | 6.3 | 5.2 |
| large_1000 | 20.5 | 14.8 | 16.1 |
| large_5000 | 102.2 | 67.9 | 83.9 |
| **realistic_calibrated** | **123.6** | **121.9** | **65.0** |

要点：

- **生产校准场景下 C++ 领先约 1.8x**（QPS 221 vs 120/124；P50 65ms vs ~122ms），这是"标杆运行时"定位的体现
- 合成 small/medium 场景 Go 吞吐最高（轻量请求路径开销最低）；大行数场景（large_1000+）Java 的 JIT 热循环优化使其反超
- 各引擎数字会随版本演进，复现方式：`scripts/bench-cross-runtime.sh --requests 10000 --concurrency 16`，报告落在 `bench-results/`

## 文档

| 类别 | 链接 |
|------|------|
| 设计文档 | [`design_doc/`](design_doc/) — 架构、数据模型、算子注册、可观测性等 |
| 算子参考 | [`doc/operators/`](doc/operators/README.md) — 所有内置算子详细说明 |
| Pipeline 编写 | [`doc/guide_pipeline.md`](doc/guide_pipeline.md) — Apple DSL 使用指南 |
| 算子开发 | [`doc/guide_operator.md`](doc/guide_operator.md) — Go 算子开发指南 |
| 第三方扩展 | [`design_doc/12_distribution.md`](design_doc/12_distribution.md) — 不修改源码添加自定义算子 |
| API 参考 | [`doc/api.md`](doc/api.md) — HTTP 接口说明 |

## License

[Apache-2.0](LICENSE)
