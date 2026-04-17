# JSON 配置格式

Apple 运行 DSL 后产出 JSON 配置文件，Pine 加载并执行。JSON 格式参考 DragonFly 的成熟方案。

## 顶层结构

```json
{
  "_PINEAPPLE_VERSION": "0.1.0",
  "_PINEAPPLE_CREATE_TIME": "2026-04-17 10:00:00",

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

所有算子存储在一个扁平 map 中，key 为算子唯一名（由 Apple 自动生成，格式为 `算子类型_HASH`）。

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

    "enrich_by_lua_D4E5F6": {
      "type_name": "lua",
      "$metadata": {
        "common_input": ["user_age"],
        "common_output": [],
        "item_input": ["item_price"],
        "item_output": ["item_adjusted_score"]
      },
      "item_defaults": {"item_price": 0.0},
      "$code_info": "pipeline.py:50 in <module>(): .enrich_by_lua(...)",
      "function_for_item": "adjust_price",
      "lua_script": "function adjust_price() if user_age < 18 then return item_price * 0.8 else return item_price end end"
    },

    "_control_G7H8I9": {
      "type_name": "lua",
      "$metadata": {
        "common_input": ["item_count"],
        "common_output": ["_if_control_1"],
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
        "common_input": ["_if_control_1"],
        "common_output": [],
        "item_input": ["item_score"],
        "item_output": ["item_rank"]
      },
      "skip": "{{_if_control_1}}"
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
| `skip` | 否 | 引用控制属性，条件为 true 时跳过该算子 |
| 其他字段 | 否 | 算子的业务参数，由算子类型定义 |

## pipeline_map — 子 flow 定义

每个子 flow 记录其包含的算子名称，按 DSL 编排顺序排列。

```json
{
  "pipeline_map": {
    "parse_sample": {
      "pipeline": [
        "filter_by_A3B2C1",
        "enrich_by_lua_D4E5F6"
      ]
    },
    "rank_and_filter": {
      "pipeline": [
        "_control_G7H8I9",
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

Pine 启动时据此校验服务层能否提供所需的 input 字段。

## 控制流的编译

DSL 中的 `if_()` / `elseif_()` / `else_()` / `end_if_()` 在 Apple 编译时被降级为普通的 Lua 算子 + skip 机制：

1. 条件表达式被编译为一个 Lua 算子（`for_branch_control: true`），输出一个隐藏的控制属性（如 `_if_control_1`）。
2. 分支内的算子在 `$metadata.common_input` 中引入该控制属性，并设置 `"skip": "{{_if_control_1}}"`。
3. Pine 执行时，控制属性为 true 则跳过该算子。

这样控制流被降级为数据依赖，不需要 Pine 引擎特殊处理——DAG 调度器天然支持。

## DAG 推导

Pine 加载 JSON 后：

1. 从 `pipeline_group` 获取子 flow 组合顺序。
2. 从 `pipeline_map` 展开各子 flow 的算子列表，得到全局算子序列。
3. 从每个算子的 `$metadata` 提取输入输出声明。
4. 按数据依赖 + DSL 顺序推导 DAG（含三种数据冒险处理）。
5. 基于 DAG 拓扑排序并行调度。
