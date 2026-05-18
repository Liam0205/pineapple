# Pineapple 计算引擎 — 概述

## 命名

| 名称 | 组件 | 语言 |
|------|------|------|
| **Pine-Go** | 主执行引擎 | Go |
| **Pine-Java** | 第二执行引擎（行为与 Pine-Go 一致） | Java |
| **Apple** | DSL 引擎 | Python |
| **Pineapple** | 以上组件协同使用的完整系统 | Go + Java + Python |

## 定位

面向 **搜索/推荐/广告** 业务的通用计算引擎。

参考系统: 快手 DragonFly 策略引擎 ([参考文档](ref_dragonfly.md))。

## 目标用户

| 角色 | 使用方式 |
|------|----------|
| 工程架构同学 | 开发新的算子 (Operator)，增强引擎能力 |
| 算法同学 | 用 Python DSL 编写业务逻辑 |

## 技术栈

| 组件 | 名称 | 技术选型 | 对比 DragonFly |
|------|------|----------|---------------|
| 执行引擎 | Pine-Go | Go | DragonFly 用 C++ |
| 执行引擎 | Pine-Java | Java | — |
| DSL 引擎 | Apple | Python | 相同 |
| 配置格式 | — | JSON | 相同 |
| 嵌入脚本 | — | Lua (Go: gopher-lua, Java: luaj) | DragonFly 无此层，靠自定义 C++ 算子解决 |

## 运行流程

```
Python DSL  ──(执行)──▶  JSON 配置文件
                              │
                   ┌──────────┴──────────┐
                   ▼                     ▼
          Pine-Go 解析配置        Pine-Java 解析配置
                   │                     │
                   ▼                     ▼
    数据驱动隐式构建 DAG      数据驱动隐式构建 DAG
                   │                     │
                   ▼                     ▼
    基于 DAG 拓扑排序并行执行  基于 DAG 拓扑排序并行执行
```

两个引擎对相同输入产生相同输出，由 CI 交叉验证保证一致性。

## 核心设计要点

1. **算子 (Operator)** 是基本计算单元，由 Go/Java 实现。分为通用算子和自定义算子。
2. **Python DSL** 是面向算法同学的声明式接口；运行 DSL 产出 JSON 配置，不参与运行时计算。
3. **JSON 配置** 是引擎与 DSL 之间的契约；引擎据此解析算子依赖，构建 DAG。
4. **数据驱动的隐式构图**: 算子声明输入/输出数据字段，引擎自动推导依赖关系和 DAG 拓扑。
5. **DAG 调度**: 无依赖算子并行执行，目标无锁设计。
6. **DataFrame**: 内置高性能表结构数据模型，提供统一的键值化数据访问接口。
7. **Lua 嵌入**: 通用算子可内嵌 Lua 运行时，在不新增 Go 算子的情况下实现特定逻辑。
8. **分层解耦**: 算法团队（DSL 之上）与架构团队（算子之下）通过 JSON 配置彻底解耦。

## 版本管理

### `_PINEAPPLE_VERSION` 字段

Apple DSL compiler 在编译时将当前 Python 包版本 (`apple._version.__version__`) 注入到 JSON 配置的 `_PINEAPPLE_VERSION` 字段。该字段为信息性标记，引擎加载时 **不做版本严格校验**（forward-compatible 策略）。

Go 侧版本常量 (`pine-go/version.go: Version`) 与 Python 侧 (`apple/_version.py: __version__`) 始终保持同步。

### 版本 bump 流程

使用 `scripts/bump-version.sh` 一条命令完成所有版本同步：

```bash
bash scripts/bump-version.sh 0.3.0
```

脚本依次执行：
1. 更新 `pine-go/version.go` Go 常量
2. 更新 `apple/_version.py` Python 版本
3. 更新 `pine-java/pom.xml` Maven 版本
4. 替换所有 `fixtures/**/*.json` 中的 `_PINEAPPLE_VERSION`
5. 重跑 codegen（`apple_generated/` + `doc/operators/`）
6. 运行 Go 测试
7. 运行 Java 测试
8. 运行 Python 测试

脚本完成后，review diff 并手动 commit + tag + push。

## 设计文档索引

- [02 流程抽象](02_flow_abstraction.md) — Flow 契约、DAG 构建与调度、Lua 算子
- [03 数据抽象](03_data_abstraction.md) — DataFrame、特征类型、数据访问接口
- [04 算子注册](04_operator_registration.md) — 注册机制、Schema、Pine↔Apple 代码生成
- [05 算子分类](05_operator_types.md) — 召回、合并、特征处理、排序、过滤、控制、观察
- [06 JSON 配置格式](06_json_config.md) — Apple 产出的 JSON 结构、控制流编译、DAG 推导
- [07 错误处理](07_error_handling.md) — 可恢复/不可恢复错误、进程保护
- [08 可观测性](08_observability.md) — 白盒回查、代码治理、debug 参数
- [09 Pine 集成模型](09_pine_integration.md) — 纯计算库定位、核心 API、配置重载
