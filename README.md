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

## 从旧版迁移（Breaking Change）

> 自 v0.7 起，Go 引擎从仓库根目录迁移至 `pine-go/` 子目录，Go module path 随之变更。

### 变更内容

| 项目 | 迁移前 | 迁移后 |
|------|--------|--------|
| Module path | `github.com/Liam0205/pineapple` | `github.com/Liam0205/pineapple/pine-go` |
| Import | `github.com/Liam0205/pineapple/internal/...` | `github.com/Liam0205/pineapple/pine-go/internal/...` |
| Import | `github.com/Liam0205/pineapple/pkg/...` | `github.com/Liam0205/pineapple/pine-go/pkg/...` |
| Import | `github.com/Liam0205/pineapple/operators` | `github.com/Liam0205/pineapple/pine-go/operators` |
| Binary | `go build ./cmd/pineapple-server` | `go build ./pine-go/cmd/pineapple-server` |

### 下游迁移步骤

```bash
# 1. 批量替换 import path
find . -name '*.go' -exec sed -i \
  's|github.com/Liam0205/pineapple/|github.com/Liam0205/pineapple/pine-go/|g' {} +

# 2. 修正 module 自身的引用（避免多余的 pine-go/pine-go）
find . -name '*.go' -exec sed -i \
  's|github.com/Liam0205/pineapple/pine-go/pine-go/|github.com/Liam0205/pineapple/pine-go/|g' {} +

# 3. 更新 go.mod
go get github.com/Liam0205/pineapple/pine-go@latest
go mod tidy
```

如果你的项目通过 `pine.NewEngine` / `pine.BuildOperator` 等公共 API 使用 Pineapple，上述步骤即可完成迁移。

### 配置与运行时语义变更

以下变更影响 JSON 配置和算子运行时行为：

#### 1. `row_dependency` 重命名为 `consumes_row_set`

JSON 配置中算子的 `"row_dependency": true` 字段已移除，改用 `"consumes_row_set": true`（语义不变：标记算子需要等待行集稳定后才执行）。

```diff
 {
   "type_name": "transform_size",
-  "row_dependency": true,
+  "consumes_row_set": true,
   "$metadata": { ... }
 }
```

Apple DSL 侧同步变更：`OpCall(..., row_dependency=True)` → `OpCall(..., consumes_row_set=True)`。

#### 2. DAG 调度模型变更：barrier → row-set marker interfaces

旧模型中 Filter/Merge/Reorder 算子被视为"barrier"——在它们执行前所有前驱必须完成，所有后继必须等它完成。

新模型通过三个 marker interface 精确声明 row-set 依赖：

| Marker | 含义 | 典型算子 |
|--------|------|----------|
| `ConsumesRowSet` | 迭代所有 item，需要行集稳定 | filter_*, merge_*, reorder_*, transform_size |
| `MutatesRowSet` | 删除或重排 item | filter_*, merge_*, reorder_* |
| `AdditiveWritesRowSet` | 追加 item（与其他追加者并行） | recall_* |

**影响**：仅操作 common 字段的 Transform 算子不再被 barrier 阻塞，可与 Filter/Merge/Reorder 并行执行。这提升了并行度但不改变最终结果——正确性由字段级数据冒险分析保证。

**自定义算子迁移**：如果你实现了自定义的 Recall 类型算子，需要嵌入 `types.AdditiveWritesRowSetMarker`。

#### 3. Field Accessor 严格模式

`BuildInput` 现在区分 Strict 和 Defaulted 字段：

- **Strict**（无 `common_defaults` / `item_defaults` 条目的字段）：运行时值为 nil 时立即报错，而非将 nil 透传给算子
- **Defaulted**（有 default 的字段）：运行时值为 nil 或缺失时替换为默认值

**影响**：如果你的流水线依赖"nil 值透传给算子由算子自行处理"的行为，需要在配置中为该字段添加 `common_defaults` 或 `item_defaults` 条目（值可以是 `null`）来保持旧行为：

```json
{
  "$metadata": { "common_input": ["optional_field"], ... },
  "common_defaults": { "optional_field": null }
}
```

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
│   ├── operators/          #   算子级单元 fixtures
│   ├── pipelines/          #   Pipeline 级端到端 fixtures
│   └── errors/             #   错误路径 fixtures
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

1. **Schema parity** — 两端 codegen 导出的算子 schema（名称、参数类型、必填项、默认值）必须一致
2. **DAG parity** — 相同配置输入，两端渲染的 DAG（DOT + Mermaid，含 collapse）必须一致
3. **Execution parity** — 相同配置 + 请求，两端执行结果（JSON 归一化后）必须一致
4. **Column-store parity** — 以列存模式重复上述执行验证
5. **Error parity** — 非法配置/请求，两端返回相同的错误分类和消息
6. **Server parity** — HTTP 端点的 status code、body 结构、Content-Type 一致
7. **Cancellation parity** — 超时和运行时错误的取消行为一致

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
