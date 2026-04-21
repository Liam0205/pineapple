# Pineapple

高性能 DAG 流水线引擎。**Python 声明，Go 执行，JSON 解耦。**

算子只需声明输入/输出字段，引擎自动推导依赖、构建 DAG、并行调度——你专注业务逻辑，Pineapple 负责把它跑快。

适用于任何需要**多步骤数据处理流水线**的场景：搜索/推荐/广告排序、特征工程、实时数据加工、规则引擎、ML 推理前后处理，等等。

## 架构概览

| 名称 | 组件 | 语言 | 职责 |
|------|------|------|------|
| **Pine** | 执行引擎 | Go | 解析配置、构建 DAG、并行调度算子 |
| **Apple** | DSL 引擎 | Python | 声明式描述业务逻辑，编译输出 JSON 配置 |
| **Pineapple** | 完整系统 | Go + Python | 二者协同，通过 JSON 配置解耦 |

```
Python DSL  ──(compile)──>  JSON 配置文件
                                │
                                v
                 Go 引擎解析 JSON，推导算子依赖
                                │
                                v
                  构建 DAG，拓扑排序，并行执行
```

**工程团队**用 Go 开发高性能算子；**业务团队**用 Python DSL 编排逻辑。两侧通过 JSON 配置彻底解耦——业务迭代不需要重编译 Go 代码，Go 服务自动热加载配置变更。

## 核心优势

**数据驱动的隐式构图** — 算子只需声明输入/输出字段，引擎自动推导 RAW/WAW/WAR 依赖关系并构建 DAG，无需手动连线。

**无锁并行调度** — DAG 中无依赖的算子自动并行执行，充分利用多核。

**编译期校验** — Python 编译器在部署前检测死代码、字段缺失、写后未读等问题，将错误拦截在开发阶段。

**Lua 嵌入扩展** — 内置 Lua 算子支持轻量级的自定义计算和条件分支，无需新增 Go 代码即可实现灵活逻辑。

**白盒可观测** — 引擎内部始终记录算子级别的执行 trace（耗时、跳过状态）。请求方通过 `common._return_trace = true` 控制是否在响应中返回 trace；默认不返回。配合算子 `debug` 参数可获取输入/输出快照，逐算子深入排查。

**动态资源管理** — `pkg/resource` 提供后台定时刷新的内存资源管理器，无锁读、刷新失败保留旧值。资源通过 `ResourceSchema` 注册，codegen 自动生成 Python 类型类，DSL 声明后编译到统一 JSON 配置。

**配置热加载** — 服务运行时监控配置文件变更，自动无停机重载，业务迭代立即生效。

**文档自动生成** — 算子和资源的 Type、Description、参数描述在注册时强制填写，codegen 自动生成 Python 类型绑定和 Markdown 文档，保证代码与文档永远同步。

**Schema 即约束** — `Register()` 强制校验算子元信息完整性，缺少 Type、Description 或参数描述则启动时直接 panic，从源头杜绝文档缺失。

## Quick Start

### 环境要求

- Go 1.22+
- Python 3.10+

### 1. 克隆项目

```bash
git clone https://github.com/Liam0205/pineapple.git
cd pineapple
go mod download
```

### 2. 编写 Python Pipeline

创建 `demo.py`（所有算子方法返回 Flow 自身，支持链式调用 `flow.recall_static(...).transform_by_lua(...).reorder_sort(...)`）：

```python
from apple.flow import Flow

flow = Flow(
    name="demo",
    common_input=["user_age"],
    item_output=["item_id", "item_final_price"],
)

# 召回：静态候选集
flow.recall_static(
    item_output=["item_id", "item_price"],
    items=[
        {"item_id": "a", "item_price": 100.0},
        {"item_id": "b", "item_price": 200.0},
        {"item_id": "c", "item_price": 50.0},
    ],
)

# 特征计算：用 Lua 根据用户年龄打折
flow.transform_by_lua(
    common_input=["user_age"],
    item_input=["item_price"],
    item_output=["item_final_price"],
    lua_script="""
function discount()
  if user_age < 18 then
    return item_price * 0.8
  else
    return item_price
  end
end
""",
    function_for_item="discount",
)

# 排序：按最终价格降序
flow.reorder_sort(
    item_input=["item_final_price"],
    field="item_final_price",
    order="desc",
)

# 编译输出 JSON 配置
with open("pipeline.json", "w") as f:
    f.write(flow.compile())

print("pipeline.json generated")
```

### 3. 生成配置

```bash
python3 demo.py
```

### 4. 启动服务

```bash
go run ./cmd/pineapple-server -config pipeline.json -addr :8080
```

### 5. 发送请求

```bash
curl -s -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{
    "common": {"user_age": 16},
    "items": []
  }' | python3 -m json.tool
```

预期返回（16 岁用户享受 8 折，结果仅含 `item_output` 声明的字段）：

```json
{
  "common": {"user_age": 16},
  "items": [
    {"item_id": "b", "item_final_price": 160.0},
    {"item_id": "a", "item_final_price": 80.0},
    {"item_id": "c", "item_final_price": 40.0}
  ],
  "trace": [...]
}
```

### 6. 迭代

修改 `demo.py` 后重新运行 `python3 demo.py`，服务自动热加载新配置，无需重启。

## 算子开发指南（工程视角）

### Operator 接口

每个算子实现两个方法：

```go
type Operator interface {
    Init(params map[string]any) error
    Execute(ctx context.Context, input *OperatorInput, output *OperatorOutput) error
}
```

- `Init`：接收业务参数，做一次性初始化
- `Execute`：每次请求调用，从 `input` 读数据、向 `output` 写数据

### 注册算子

在 `init()` 中调用 `pine.Register()`，所有元信息字段**必填**：

```go
package myop

import (
    "context"
    pine "github.com/Liam0205/pineapple"
)

func init() {
    pine.Register(pine.OperatorSchema{
        Name:        "transform_my_custom",
        Type:        pine.OpTypeTransform,
        Description: "Computes a custom feature for each item.",
        Params: map[string]pine.ParamSpec{
            "field":  {Type: "string", Required: true, Description: "Input field name."},
            "factor": {Type: "float64", Required: false, Default: 1.0, Description: "Scaling factor."},
        },
    }, func() pine.Operator {
        return &MyCustomOp{}
    })
}

type MyCustomOp struct {
    field  string
    factor float64
}

func (o *MyCustomOp) Init(params map[string]any) error {
    o.field = params["field"].(string)
    o.factor = params["factor"].(float64)
    return nil
}

func (o *MyCustomOp) Execute(_ context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    for i := 0; i < in.ItemCount(); i++ {
        val := in.Item(i, o.field).(float64)
        out.SetItem(i, o.field+"_scaled", val*o.factor)
    }
    return nil
}
```

#### Schema 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Name` | string | Yes | 算子唯一标识，蛇形命名，前缀体现类型（`recall_`/`transform_`/`filter_`/`merge_`/`reorder_`/`observe_`） |
| `Type` | OperatorType | Yes | 类型（Recall / Transform / Filter / Merge / Reorder / Observe） |
| `Description` | string | Yes | 一句话功能描述 |
| `Params[k].Type` | string | Yes | `"string"` / `"int64"` / `"float64"` / `"bool"` / `"any"` |
| `Params[k].Required` | bool | Yes | 是否必填 |
| `Params[k].Default` | any | No | 可选参数的默认值 |
| `Params[k].Description` | string | Yes | 参数描述 |

> 缺少 `Type`、`Description` 或任一参数的 `Description` 将导致启动 panic。

### 注释中的 Metadata Contract（可选）

在源文件顶部添加 `Metadata contract` 注释段，codegen 会将其解析到文档中：

```go
// Operator: transform_my_custom
// Type: Transform
// ...
//
// Metadata contract (typical usage):
//   CommonInput:  []
//   CommonOutput: []
//   ItemInput:    [<field>]
//   ItemOutput:   [<field>_scaled]
package myop
```

### 生成代码和文档

```bash
# 生成 Python DSL 绑定 + 算子文档
go run ./cmd/pineapple-codegen \
  -output apple_generated \
  -doc-dir doc/operators \
  -operators-dir operators
```

这将自动生成：
- `apple_generated/operators.py` — 带类型提示的 Python 算子类
- `apple_generated/__init__.py` — 算子导出列表
- `apple_generated/resources.py` — 带类型提示的 Python 资源类（若有注册资源）
- `apple_generated/resources_init.py` — 资源导出列表（若有注册资源）
- `doc/operators/<name>.md` — 每个算子的文档
- `doc/operators/README.md` — 按分类索引

### 测试

```bash
# 单个算子包
go test ./operators/transform/...

# 全量测试
go test ./...
```

### 动态资源管理

资源（特征索引、AB 配置等需要定时刷新的数据）与算子对称，走 **Go Schema → codegen Python 类 → DSL 声明 → 编译到统一 JSON** 的全流程。

#### 注册资源 Schema

在 `init()` 中调用 `pine.RegisterResource()`：

```go
package myresource

import (
    pine "github.com/Liam0205/pineapple"
    "github.com/Liam0205/pineapple/pkg/resource"
)

func init() {
    pine.RegisterResource(pine.ResourceSchema{
        Name:            "feature_index",
        Description:     "User feature lookup table.",
        DefaultInterval: 600,  // 刷新间隔（秒），0 → 默认 10min
        Params: map[string]pine.ParamSpec{
            "dsn": {Type: "string", Required: true, Description: "Database DSN."},
        },
    }, func(params map[string]any) (resource.Fetcher, error) {
        dsn := params["dsn"].(string)
        return newFeatureIndexFetcher(dsn), nil
    })
}
```

#### ResourceSchema 字段说明

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `Name` | string | Yes | 资源唯一标识，蛇形命名 |
| `Description` | string | Yes | 一句话功能描述 |
| `DefaultInterval` | int | No | 刷新间隔（秒），0 → 默认 10min |
| `Params[k]` | ParamSpec | — | 与算子共用 `ParamSpec`（Type、Required、Default、Description） |

#### DSL 中声明资源

codegen 会生成带类型的 Python 资源类。在 pipeline 文件中：

```python
from apple_generated.resources import FeatureIndexResource

flow.resource("my_index", FeatureIndexResource(dsn="host:3306/db"))
```

编译后，统一 JSON 配置包含 `resource_config`：

```json
{
  "pipeline_config": { ... },
  "pipeline_group": { ... },
  "flow_contract": { ... },
  "resource_config": {
    "my_index": {
      "type": "feature_index",
      "interval": 600,
      "params": {"dsn": "host:3306/db"}
    }
  }
}
```

#### 算子中读取资源

```go
import "github.com/Liam0205/pineapple/pkg/resource"

func (o *MyOp) Execute(ctx context.Context, in *pine.OperatorInput, out *pine.OperatorOutput) error {
    rp := resource.FromContext(ctx)
    if rp == nil {
        return nil // 未注入，降级处理
    }
    idx, ok := rp.Get("my_index")
    if !ok {
        return nil // 资源未就绪，降级
    }
    table := idx.(*FeatureTable)
    // 使用 table ...
    return nil
}
```

详见 [动态资源管理设计文档](design_doc/11_resource_manager.md)。

## Pipeline 编写指南（算法视角）

### 基本用法

```python
from apple.flow import Flow

flow = Flow(
    name="my_pipeline",
    common_input=["user_id", "user_age"],   # 请求级上下文字段
    item_output=["item_id", "item_score"],  # 最终输出字段
)
```

### 链式调用算子

```python
# 召回候选集
flow.recall_static(
    item_output=["item_id", "item_score"],
    items=[...],
)

# 过滤
flow.filter_condition(
    item_input=["item_status"],
    field="item_status",
    value="offline",
)

# 特征处理
flow.transform_normalize(
    item_input=["item_score"],
    item_output=["item_score_norm"],
    field="item_score",
)

# 截断
flow.filter_truncate(
    top_n=50,
)

# 排序
flow.reorder_sort(
    item_input=["item_score_norm"],
    field="item_score_norm",
    order="desc",
)
```

### 条件分支

```python
flow.if_("is_new_user") \
    .transform_dispatch(
        common_input=["default_score"],
        item_output=["item_score"],
        common_field="default_score",
        item_field="item_score",
    ) \
.else_() \
    .transform_by_lua(
        common_input=["user_id"],
        item_input=["item_id"],
        item_output=["item_score"],
        lua_script="...",
        function_for_item="score",
    ) \
.end_if_()
```

### SubFlow 复用

```python
from apple.flow import Flow, SubFlow

normalize_sub = SubFlow(name="normalize")
normalize_sub.transform_normalize(
    item_input=["raw_score"],
    item_output=["norm_score"],
    field="raw_score",
)

flow = Flow(
    name="main",
    common_input=["user_id"],
    item_output=["item_id", "norm_score"],
    sub_flows=[normalize_sub],
)
flow.recall_static(...)
# normalize_sub 的算子会被展开到 flow 中
```

### 资源声明

当算子依赖外部数据时，在 pipeline 中声明资源。codegen 会为每种资源生成带类型的 Python 类：

```python
from apple_generated.resources import FeatureIndexResource

flow = Flow(name="my_pipeline", ...)

# 声明资源
flow.resource("my_index", FeatureIndexResource(dsn="host:3306/db"))

# 算子引用资源（通过 resource_name 参数）
flow.recall_feature_index(
    resource_name="my_index",
    item_output=["item_id", "score"],
)
```

编译器会校验所有 `resource_name` 引用是否有匹配的资源声明，未声明即报错。

### Metadata 声明

每个算子调用需要声明它读写的字段：

| 参数 | 含义 |
|------|------|
| `common_input` | 读取的请求级字段 |
| `common_output` | 写入的请求级字段 |
| `item_input` | 读取的物品级字段 |
| `item_output` | 写入的物品级字段 |
| `item_defaults` | 物品级字段默认值 |
| `common_defaults` | 请求级字段默认值 |
| `sources` | 合并算子的数据来源 |
| `debug` | 启用此算子的调试快照 |

> Recall 身份由算子名前缀 (`recall_`) 自动推导，无需手动声明。

### 编译和校验

```python
# 编译为 JSON 字符串
json_str = flow.compile()

# 编译为 dict（不写文件）
config = flow.compile_dict()
```

编译器自动执行以下校验：

- **字段覆盖** — 算子声明读取的字段必须有上游产出
- **死代码检测** — 产出字段未被下游消费的算子会被标记（observe 类算子豁免）
- **写后覆写** — 检测同一字段被多次写入
- **控制流完整性** — `if_` 必须有对应的 `end_if_`
- **空分支检测** — 控制块的每个分支下必须有至少一个业务算子，空分支（如 `if_("cond").end_if_()`）会报错

## API 参考

### `POST /execute`

执行 pipeline。

**请求体：**

```json
{
  "common": {"user_id": "123", "user_age": 25},
  "items": []
}
```

**响应体：**

```json
{
  "common": {"user_id": "123", "user_age": 25},
  "items": [
    {"item_id": "a", "item_score": 0.95}
  ],
  "warnings": [],
  "trace": [
    {
      "name": "recall_static_ABA9A7",
      "duration_ms": 0.123,
      "skipped": false
    }
  ]
}
```

### `GET /health`

健康检查。返回 `{"status": "ok"}`。

### `GET /stats`

引擎运行统计（请求计数、算子执行次数和耗时分布等）。

## 项目结构

```
pineapple/
├── apple/                  # Python DSL (Apple)
│   ├── base.py             #   算子基类
│   ├── resource.py         #   资源基类 + ResourceDecl
│   ├── flow.py             #   Flow/SubFlow 声明（含资源声明）
│   ├── compiler.py         #   编译器：DSL -> JSON（含 resource_config）
│   ├── validator.py        #   静态校验器
│   ├── control.py          #   控制流 (if/else) 支持
│   ├── generated/          #   自动生成的 Python 绑定
│   └── tests/              #   Python 测试
├── cmd/
│   ├── pineapple-server/   # HTTP 服务入口
│   └── pineapple-codegen/  # 代码 & 文档生成工具
├── pkg/
│   ├── resource/           # 动态资源管理 (ResourceManager)
│   ├── server/             # 可复用 HTTP 服务库
│   └── codegen/            # 可复用代码生成库
├── design_doc/             # 设计文档 (01-12)
├── doc/
│   ├── operators/          # 自动生成的算子文档
│   └── reports/            # 测试 & 性能报告
├── internal/               # Go 内部包
│   ├── config/             #   JSON 配置解析
│   ├── dag/                #   DAG 构建与拓扑排序
│   ├── dataframe/          #   DataFrame 实现
│   ├── registry/           #   算子注册表
│   ├── runtime/            #   调度器、trace、stats
│   └── types/              #   核心类型定义
├── operators/              # 内置算子实现
│   ├── transform/          #   transform_dispatch, transform_normalize
│   ├── filter/             #   filter_condition, filter_truncate
│   ├── lua/                #   transform_by_lua (Lua 嵌入)
│   ├── merge/              #   merge_dedup
│   ├── observe/            #   observe_log
│   ├── recall/             #   recall_static
│   └── reorder/            #   reorder_sort
├── integration/            # 集成测试
├── benchmarks/             # 性能基准测试
└── testdata/               # 测试用 JSON 配置
```

## 文档链接

- **设计文档**
  - [概述](design_doc/01_overview.md)
  - [流程抽象](design_doc/02_flow_abstraction.md)
  - [数据抽象](design_doc/03_data_abstraction.md)
  - [算子注册](design_doc/04_operator_registration.md)
  - [算子分类](design_doc/05_operator_types.md)
  - [JSON 配置格式](design_doc/06_json_config.md)
  - [错误处理](design_doc/07_error_handling.md)
  - [可观测性](design_doc/08_observability.md)
  - [Pine 集成模型](design_doc/09_pine_integration.md)
  - [文档自动生成](design_doc/10_docgen.md)
  - [动态资源管理](design_doc/11_resource_manager.md)
  - [发布与第三方扩展](design_doc/12_distribution.md)

- **[算子参考文档](doc/operators/README.md)** — 所有内置算子的详细说明、参数、用法示例

## 第三方扩展

第三方项目可以在**不修改 pineapple 源码**的前提下添加自定义算子和资源。核心思路：写自己的算子/资源包，通过 blank import 注册到全局 registry，然后用 `pkg/server` 和 `pkg/codegen` 构建自己的服务和 Python 绑定。

```
my-project/
├── go.mod                    # require github.com/Liam0205/pineapple
├── operators/
│   └── my_scorer/
│       └── scorer.go         # init() { pine.Register(schema, factory) }
├── resources/
│   └── feature_index/
│       └── feature_index.go  # init() { pine.RegisterResource(schema, factory) }
├── cmd/
│   ├── my-server/
│   │   └── main.go           # import 算子 + 资源 → server.Run()
│   └── my-codegen/
│       └── main.go           # import 算子 + 资源 → codegen.Run()
├── apple_generated/          # codegen 产出（算子 + 资源的 Python 绑定）
└── pipelines/
    └── my_pipeline.py        # DSL 声明算子 + 资源 → 编译统一 JSON
```

### Server wrapper 示例

```go
package main

import (
    "flag"
    "log"

    _ "github.com/Liam0205/pineapple/operators" // 内置算子
    _ "my-project/operators/my_scorer"            // 自定义算子
    _ "my-project/resources/feature_index"        // 自定义资源
    "github.com/Liam0205/pineapple/pkg/server"
)

func main() {
    configPath := flag.String("config", "", "Unified JSON config (pipeline + resources)")
    addr := flag.String("addr", ":8080", "Listen address")
    flag.Parse()
    if err := server.Run(server.Config{
        ConfigPath: *configPath,
        Addr:       *addr,
    }); err != nil {
        log.Fatal(err)
    }
}
```

### Codegen wrapper 示例

```go
package main

import (
    "flag"
    "fmt"
    "os"

    _ "github.com/Liam0205/pineapple/operators"
    _ "my-project/operators/my_scorer"
    _ "my-project/resources/feature_index"
    "github.com/Liam0205/pineapple/pkg/codegen"
)

func main() {
    output := flag.String("output", "apple_generated", "Python output dir")
    docDir := flag.String("doc-dir", "", "Doc output dir")
    opsDir := flag.String("operators-dir", "operators", "Go operators source")
    flag.Parse()
    if err := codegen.Run(codegen.Config{OutputDir: *output, DocDir: *docDir, OpsDir: *opsDir}); err != nil {
        fmt.Fprintf(os.Stderr, "codegen: %v\n", err)
        os.Exit(1)
    }
}
```

详见 [发布与第三方扩展设计文档](design_doc/12_distribution.md)。
