# JSON 配置格式

Apple 运行 DSL 后产出 JSON 配置文件，Pine 加载并执行。JSON 格式参考 DragonFly 的成熟方案。

## 顶层结构

```json
{
  "_PINEAPPLE_VERSION": "0.5.0",
  "_PINEAPPLE_CREATE_TIME": "2026-04-17T10:00:00Z",

  "pipeline_config": {
    "operators": { ... },
    "pipeline_map": { ... }
  },

  "pipeline_group": { ... },

  "flow_contract": { ... }
}
```

| 字段 | 说明 |
|------|------|
| `_PINEAPPLE_VERSION` | Apple 版本号 |
| `_PINEAPPLE_CREATE_TIME` | 配置生成时间 |
| `pipeline_config.operators` | 所有算子的扁平 map |
| `pipeline_config.pipeline_map` | 子 flow 定义（算子执行顺序） |
| `pipeline_group` | 顶层 pipeline 定义（子 flow 组合顺序） |
| `flow_contract` | Flow 的输入输出契约 |

## operators — 算子定义

所有算子存储在一个扁平 map 中，key 为算子唯一名。默认由 Apple 自动生成（格式为 `算子类型_HASH`），用户也可通过 `name=` 参数显式指定：

```python
# 自动命名 → recall_static_A1B2C3
flow.recall_static(item_output=["item_id"], items=[...], recall=True)

# 显式命名 → my_recall
flow.recall_static(name="my_recall", item_output=["item_id"], items=[...], recall=True)
```

显式名称在整个 pipeline 中必须唯一，重复时编译报错。

```json
{
  "operators": {
    "filter_by_A3B2C1": {
      "type_name": "filter",
      "$metadata": {
        "common_input": [],
        "common_output": [],
        "item_input": ["item_status"],
        "item_output": []
      },
      "$code_info": "pipeline.py:42 in <module>(): .filter_by(...)",
      "remove_if": "item_status == 'offline'"
    },

    "transform_by_lua_D4E5F6": {
      "type_name": "transform_by_lua",
      "$metadata": {
        "common_input": ["user_age"],
        "common_output": [],
        "item_input": ["item_price"],
        "item_output": ["item_adjusted_score"]
      },
      "item_defaults": {"item_price": 0.0},
      "$code_info": "pipeline.py:50 in <module>(): .transform_by_lua(...)",
      "function_for_item": "adjust_price",
      "lua_script": "function adjust_price() if user_age < 18 then return item_price * 0.8 else return item_price end end"
    },

    "if_1": {
      "type_name": "transform_by_lua",
      "$metadata": {
        "common_input": ["item_count"],
        "common_output": ["_if_1"],
        "item_input": [],
        "item_output": []
      },
      "$code_info": "[if] pipeline.py:60: .if_(\"item_count > 0\")",
      "function_for_common": "evaluate",
      "lua_script": "function evaluate() if (item_count > 0) then return false else return true end end",
      "for_branch_control": true
    },

    "some_op_J1K2L3": {
      "type_name": "some_op",
      "$metadata": {
        "common_input": ["_if_1"],
        "common_output": [],
        "item_input": ["item_score"],
        "item_output": ["item_rank"]
      },
      "skip": "_if_1"
    }
  }
}
```

### 算子字段说明

| 字段 | 必选 | 说明 |
|------|------|------|
| `type_name` | 是 | Pine 侧注册的算子类型名 |
| `$metadata` | 是 | 输入输出声明，Pine 据此推导 DAG |
| `$metadata.common_input` | 是 | 读取的 common 字段列表 |
| `$metadata.common_output` | 是 | 写入的 common 字段列表 |
| `$metadata.item_input` | 是 | 读取的 item 字段列表 |
| `$metadata.item_output` | 是 | 写入的 item 字段列表 |
| `$code_info` | 否 | DSL 源码位置，便于调试回溯 |
| `skip` | 否 | 引用控制属性（common 字段名），值为 true 时跳过该算子 |
| `recall` | 否 | 标记为召回算子，item_output 不参与字段级 DAG 推导，引擎写回时自动注入 `_source` |
| `sources` | 否 | 合并算子专用，引用召回算子名称，Pine 据此建立显式 DAG 边 |
| 其他字段 | 否 | 算子的业务参数，由算子类型定义 |

### 召回与合并算子示例

```json
{
  "recall_from_index_A1B2C3": {
    "type_name": "recall_from_index",
    "recall": true,
    "$metadata": {
      "common_input": ["user_id", "query_embedding"],
      "common_output": [],
      "item_input": [],
      "item_output": ["item_id", "item_score", "item_category"]
    },
    "$code_info": "pipeline.py:10 in <module>(): .recall_from_index(...)",
    "index_name": "main_index",
    "top_k": 1000
  },

  "recall_from_realtime_D4E5F6": {
    "type_name": "recall_from_realtime",
    "recall": true,
    "$metadata": {
      "common_input": ["user_id"],
      "common_output": [],
      "item_input": [],
      "item_output": ["item_id", "item_score"]
    },
    "$code_info": "pipeline.py:18 in <module>(): .recall_from_realtime(...)",
    "service_name": "realtime_index",
    "top_k": 500
  },

  "merge_G7H8I9": {
    "type_name": "merge",
    "$metadata": {
      "common_input": [],
      "common_output": [],
      "item_input": [],
      "item_output": ["item_id", "item_score", "item_category"]
    },
    "$code_info": "pipeline.py:25 in <module>(): .merge(...)",
    "sources": ["recall_from_index_A1B2C3", "recall_from_realtime_D4E5F6"],
    "dedup_by": "item_id",
    "strategy": "union"
  }
}
```

Pine 处理召回/合并的逻辑：

1. 识别 `"recall": true` 的算子，其 `item_output` **不参与字段级 DAG 推导**（避免多个并行召回因同名字段被串行化）。
2. 召回算子执行后，引擎通过 `AddItem` 将结果直接写入主 DataFrame，并自动注入 `_source` 字段（值为算子名）。
3. 识别 merge 算子的 `"sources"` 字段，建立 source → merge 的显式 DAG 边（不走字段名推导）。
4. merge 执行时读取主 DataFrame 中所有 item（含 `_source`），按策略去重、合并字段后，通过 `RemoveItem` 移除重复行、通过 `SetItem` 更新合并后的字段值。

## pipeline_map — 子 flow 定义

每个子 flow 记录其包含的算子名称，按 DSL 编排顺序排列。

```json
{
  "pipeline_map": {
    "parse_sample": {
      "pipeline": [
        "filter_by_A3B2C1",
        "transform_by_lua_D4E5F6"
      ]
    },
    "rank_and_filter": {
      "pipeline": [
        "if_1",
        "some_op_J1K2L3"
      ]
    }
  }
}
```

## pipeline_group — 顶层 pipeline 组合

定义子 flow 的组合顺序。

```json
{
  "pipeline_group": {
    "main": {
      "pipeline": [
        "parse_sample",
        "rank_and_filter"
      ]
    }
  }
}
```

## flow_contract — Flow 输入输出契约

```json
{
  "flow_contract": {
    "common_input": ["user_age", "user_id"],
    "item_input": ["item_id", "item_status", "item_price", "item_score"],
    "common_output": [],
    "item_output": ["item_rank", "item_adjusted_score"]
  }
}
```

Pine 启动时据此校验服务层能否提供所需的 input 字段。执行完毕后，Engine 根据 `common_output` 和 `item_output` 对结果做投影——只返回声明的字段，过滤掉中间计算产生的临时字段（如 `_source`）。未声明输出（列表为空）则不返回任何字段。

## 控制流的编译

DSL 中的 `if_()` / `elseif_()` / `else_()` / `end_if_()` 在 Apple 编译时被降级为普通的 Lua 算子 + skip 机制。Pine 无需特殊处理控制流——DAG 调度器天然支持。

### 基本原理

每个条件分支被编译为一个 Lua 控制算子（`for_branch_control: true`），输出一个隐藏的控制属性（common 字段，`_` 前缀）。分支内的算子通过 `skip` 字段引用该控制属性的字段名，Pine 在执行前读取该字段值，为 true 时跳过算子。

**skip 语义约定：`skip` 的值是一个 common 字段名。运行时该字段值为 true 表示"跳过"，false 表示"执行"。**

### 简单 if

```python
flow.if_("item_count > 0") \
    .some_op(...) \
.end_if_()
```

编译为：
1. 生成控制算子 `if_1`（显式命名，底层 `type_name` 仍为 `transform_by_lua`），输出 `_if_1`。
2. 条件为 true 时 `_if_1 = false`（不跳过分支），条件为 false 时 `_if_1 = true`（跳过分支）。
3. 分支内算子设置 `"skip": "_if_1"`。

### if / elseif / else

```python
flow.if_("cond_A") \
    .op_for_A(...) \
.elseif_("cond_B") \
    .op_for_B(...) \
.else_() \
    .op_for_default(...) \
.end_if_()
```

编译为链式控制属性：

1. **`if_("cond_A")`** → 控制算子 `if_1`
   - 输入：`cond_A` 引用的 common 字段
   - 输出：`_if_1`
   - 逻辑：`cond_A` 为 true → `_if_1 = false`；否则 `_if_1 = true`
   - 分支内算子：`"skip": "_if_1"`

2. **`elseif_("cond_B")`** → 控制算子 `elseif_2`
   - 输入：`_if_1` + `cond_B` 引用的 common 字段
   - 输出：`_elif_2`
   - 逻辑：当 `_if_1 == true`（前面分支未命中）**且** `cond_B` 为 true → `_elif_2 = false`；否则 `_elif_2 = true`
   - 分支内算子：`"skip": "_elif_2"`

3. **`else_()`** → 控制算子 `else_3`
   - 输入：`_if_1, _elif_2`
   - 输出：`_else_3`
   - 逻辑：当所有前置控制属性均为 true（所有前面分支均未命中）→ `_else_3 = false`；否则 `_else_3 = true`
   - 分支内算子：`"skip": "_else_3"`

关键性质：**三个分支互斥，恰好有一个分支执行。** 这由链式依赖保证——每个 `elseif` / `else` 的控制算子读取前面所有控制属性，确保只有"前面都没命中"时才可能执行当前分支。

### JSON 示例：if / elseif / else

```json
{
  "if_1": {
    "type_name": "transform_by_lua",
    "$metadata": {
      "common_input": ["item_count"],
      "common_output": ["_if_1"],
      "item_input": [],
      "item_output": []
    },
    "$code_info": "[if] item_count > 0",
    "function_for_common": "evaluate",
    "lua_script": "function evaluate() if (item_count > 0) then return false else return true end end",
    "for_branch_control": true
  },

  "op_for_A_D4E5F6": {
    "type_name": "some_op",
    "$metadata": {
      "common_input": ["_if_1"],
      "common_output": [],
      "item_input": ["item_score"],
      "item_output": ["item_rank"]
    },
    "skip": "_if_1"
  },

  "elseif_2": {
    "type_name": "transform_by_lua",
    "$metadata": {
      "common_input": ["_if_1", "fallback_enabled"],
      "common_output": ["_elif_2"],
      "item_input": [],
      "item_output": []
    },
    "$code_info": "[elseif] fallback_enabled ~= nil",
    "function_for_common": "evaluate",
    "lua_script": "function evaluate() if (_if_1 == true) and (fallback_enabled ~= nil) then return false else return true end end",
    "for_branch_control": true
  },

  "op_for_B_J1K2L3": {
    "type_name": "fallback_op",
    "$metadata": {
      "common_input": ["_elif_2"],
      "common_output": [],
      "item_input": ["item_id"],
      "item_output": ["item_fallback_score"]
    },
    "skip": "_elif_2"
  },

  "else_3": {
    "type_name": "transform_by_lua",
    "$metadata": {
      "common_input": ["_if_1", "_elif_2"],
      "common_output": ["_else_3"],
      "item_input": [],
      "item_output": []
    },
    "$code_info": "[else] ",
    "function_for_common": "evaluate",
    "lua_script": "function evaluate() if (_if_1 == true) and (_elif_2 == true) then return false else return true end end",
    "for_branch_control": true
  },

  "op_for_default_P7Q8R9": {
    "type_name": "default_op",
    "$metadata": {
      "common_input": ["_else_3"],
      "common_output": [],
      "item_input": ["item_id"],
      "item_output": ["item_default_score"]
    },
    "skip": "_else_3"
  }
}
```

### 嵌套 if

嵌套 if 天然支持。内层控制算子通过 `common_input` 读取外层控制属性，在 Lua 脚本中将外层条件吸收进判断逻辑，输出的控制属性已包含"外层命中且内层命中"的综合结果。因此 `skip` 始终只需引用一个字段名。

```python
flow.if_("cond_outer") \
    .outer_op(...) \
    .if_("cond_inner") \
        .inner_op(...) \
    .else_() \
        .inner_else_op(...) \
    .end_if_() \
.end_if_()
```

编译结果：

| 算子 | skip | 控制算子逻辑 |
|------|------|-------------|
| `outer_op` | `_if_1` | — |
| `inner_op` | `_if_2` | `if_2` 输入 `_if_1`：外层命中（`_if_1 == false`）**且** `cond_inner` 成立 → `_if_2 = false` |
| `inner_else_op` | `_else_3` | `else_3` 输入 `_if_1, _if_2`：外层命中 **且** 内层未命中 → `_else_3 = false` |

这与 `elseif` 的链式依赖是同一个模式——Apple 在编译时处理嵌套关系，Pine 侧无感知。

### 设计优势

- **Pine 零成本**：控制流完全降级为数据依赖，DAG 调度器天然支持，引擎无需实现分支逻辑。
- **可观测**：控制属性在 DataFrame 中可见（以 `_` 前缀标记为内部字段），调试时可直接查看每个分支的命中情况。
- **可扩展**：如果未来需要更复杂的控制流（如 switch-case），同样可以用控制属性 + skip 机制编译。
- **算子透明**：引擎在注入 metadata 和构建算子输入时自动过滤 `skip` 引用的控制字段，算子业务逻辑不感知控制属性的存在。DAG 依赖推导仍使用完整的 `$metadata`（含控制字段）。

### 编译期校验

- **未关闭控制块**：`if_()` 必须有配对的 `end_if_()`，否则编译时报错（`ValidationError`）。
- **空分支检测**：`end_if_()` 时检查每个分支下是否有至少一个业务算子引用了该分支的 `ctrl_field`。空分支（如 `if_("cond").end_if_()`）会立即报错（`ValueError`），因为空分支生成的控制算子无意义。

## DAG 推导

Pine 加载 JSON 后：

1. 从 `pipeline_group` 获取子 flow 组合顺序。
2. 从 `pipeline_map` 展开各子 flow 的算子列表，得到全局算子序列。
3. 从每个算子的 `$metadata` 提取输入输出声明。
4. 对 `recall: true` 的算子，其 `item_output` **不参与**字段级依赖推导（允许多路召回并行）。
5. 按数据依赖 + DSL 顺序推导 DAG（含三种数据冒险处理）。
6. 对 merge 算子的 `sources` 字段，建立 source → merge 的显式 DAG 边。
7. 传递性归约：移除所有被更长路径隐含的冗余边，保留保持可达性的最小边集。
8. 基于 DAG 拓扑排序并行调度。
