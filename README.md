[English](README-en.md) | 简体中文

# Pineapple

高性能 DAG 流水线引擎。**Python 声明，Go/Java 执行，JSON 解耦。**

算子只需声明输入/输出字段，引擎自动推导依赖、构建 DAG、并行调度——你专注业务逻辑，Pineapple 负责把它跑快。

适用于任何需要**多步骤数据处理流水线**的场景：搜索/推荐/广告排序、特征工程、实时数据加工、规则引擎、ML 推理前后处理，等等。

> **⚠️ Pre-1.0 阶段**：API 和行为语义可能在版本间发生不兼容变更。生产环境使用请锁定具体版本。

## 架构

```
Python DSL (Apple)  ──compile──>  JSON Config
                                      │
                          ┌───────────┴───────────┐
                          v                       v
                   Pine-Go (Go)            Pine-Java (Java)
                   构建 DAG、并行执行       构建 DAG、并行执行
```

| 组件 | 语言 | 职责 |
|------|------|------|
| **Apple** | Python | 声明式 DSL，编译输出 JSON 配置 |
| **Pine-Go** | Go | 主执行引擎：解析配置、构建 DAG、并行调度 |
| **Pine-Java** | Java | 第二执行引擎，与 Pine-Go 行为一致 |

**工程团队**用 Go/Java 开发高性能算子；**业务团队**用 Python DSL 编排逻辑。两侧通过 JSON 配置彻底解耦。

## 核心特性

- **隐式构图** — 算子声明输入/输出字段，引擎自动推导 DAG 依赖并执行传递性归约
- **无锁并行** — DAG 中无依赖的算子自动并行执行
- **编译期校验** — 死代码、字段缺失、写后未读等问题在部署前拦截
- **Lua 嵌入** — 内置 Lua 算子支持轻量自定义计算，仅比 Go 原生慢约 1.3x
- **配置热加载** — 服务运行时自动无停机重载引擎配置
- **动态资源** — 后台定时刷新的内存资源管理器，无锁读
- **白盒可观测** — 算子级 trace、`/stats` 端点、可插拔 Prometheus 接口
- **行存/列存可切换** — DataFrame 支持两种存储模式
- **双引擎一致性** — Go/Java 引擎通过 CI 交叉验证保证 schema、DAG、执行结果一致

## Quick Start

### 环境要求

- Go 1.22+（Pine-Go）
- Java 11+（Pine-Java）
- Python 3.10+（Apple DSL）

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
├── fixtures/               # 共享测试 fixtures（Go/Java 公用）
│   └── pipelines/          #   Pipeline 级 fixture
├── scripts/                # 开发者脚本
├── design_doc/             # 设计文档
└── doc/                    # 生成的算子文档 & 报告
```

## 开发

### 常用脚本

| 脚本 | 用途 |
|------|------|
| `scripts/go-test.sh` | Go 全量测试 |
| `scripts/java-test.sh` | Java 全量测试 |
| `scripts/test-all.sh` | Go + Java + Python 全量测试 |
| `scripts/lint.sh` | Go + Java + Python lint |
| `scripts/go-bench.sh` | Go 性能基准 |
| `scripts/java-bench.sh` | Java 性能基准 |
| `scripts/go-fuzz.sh` | Go fuzz 测试 |
| `scripts/java-fuzz.sh` | Java fuzz 测试 |
| `scripts/cross-validate.sh` | Go/Java 交叉验证（schema + DAG + 执行） |
| `scripts/codegen.sh` | 代码生成（支持 `--backend go\|java`） |
| `scripts/render-dag.sh` | DAG 可视化（支持 `--backend go\|java`） |
| `scripts/apple-compile.sh` | Apple DSL 编译为 JSON |
| `scripts/run-pipeline.sh` | 单次执行 pipeline |
| `scripts/bump-version.sh` | 版本号同步更新 |

### CI 流水线

CI 在每次 push/PR 时自动运行：

- **Lint** — Go (golangci-lint)、Java (checkstyle)、Python (ruff)
- **Test** — Go/Java/Python 全量测试 + 覆盖率
- **Fuzz** — Go/Java fuzz 测试
- **Benchmark** — Go/Java 性能基准
- **Cross-validation** — Go/Java schema parity + DAG parity + 执行结果一致性
- **Codegen check** — 确保生成代码与源码同步

### 交叉验证

`scripts/cross-validate.sh` 验证 Go 和 Java 引擎的一致性：

1. **Schema parity** — 两端 codegen 导出的算子 schema（名称、参数类型、必填项）必须一致
2. **DAG parity** — 相同配置输入，两端渲染的 DAG（DOT 格式）必须一致
3. **Execution parity** — 相同配置 + 请求，两端执行结果（JSON 归一化后）必须一致

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

[MIT](LICENSE)
