---
name: cross-engine-operator
description: 在下游构造跨引擎一致的算子，确保各语言版本的 Pine 引擎行为一致。
---

# cross-engine-operator

指导如何开发一个新算子，使其在所有语言版本的 Pine 引擎中行为一致。

## 算子开发流程

### Phase 1: 设计 — Fixture First

在写任何代码之前，先定义算子的行为规范：

#### 1.1 编写 operator fixture

创建 `fixtures/operators/<operator_name>.json`：

```json
{
  "operator": "my_new_operator",
  "cases": [
    {
      "name": "basic case — describe what this tests",
      "params": {
        "param1": "value1",
        "param2": 42
      },
      "metadata": {
        "common_input": ["field_a"],
        "item_input": ["field_b", "field_c"],
        "common_output": ["field_a"],
        "item_output": ["field_b", "field_c", "new_field"]
      },
      "input": {
        "common": { "field_a": "hello" },
        "items": [
          { "field_b": 1, "field_c": "x" },
          { "field_b": 2, "field_c": "y" }
        ]
      },
      "expected": {
        "common": { "field_a": "hello" },
        "items": [
          { "field_b": 1, "field_c": "x", "new_field": "computed" },
          { "field_b": 2, "field_c": "y", "new_field": "computed" }
        ]
      }
    }
  ]
}
```

**关键原则**:
- 覆盖 golden path + edge cases (空输入、缺失字段、边界值)
- metadata 声明字段依赖（用于 DAG 依赖分析和 data_parallel 切分）
- 数值表示统一 (fixture 中整数写 `83` 不写 `83.0`)

#### 1.2 设计 schema

算子参数的 schema 定义（用于 codegen 和 config 校验）：

```
Operator: my_new_operator
Params:
  - param1: string, required
  - param2: int, optional, default=10
  - fail_on_error: bool, optional, default=false
```

#### 1.3 编写 pipeline fixture（如需要）

如果算子与其他算子组合时有特殊行为，创建 `fixtures/pipelines/<scenario>.json`：

```json
{
  "config": {
    "operators": {
      "step1": { "operator": "transform_set", "params": {...} },
      "step2": { "operator": "my_new_operator", "params": {...} }
    },
    "pipeline": ["step1", "step2"]
  },
  "cases": [...]
}
```

### Phase 2: 实现 — 逐引擎落地

#### 2.1 Go 实现（ground truth）

Go 是参考实现，其行为定义了 fixture 的 expected 输出。

```
pine-go/operators/my_new_operator.go
```

实现要点：
1. 实现 `Operator` 接口：`Init(params)`, `Process(frame)`, `Schema() OperatorSchema`
2. 在 `registry.go` 中注册
3. 运行 operator fixture test 确认通过
4. 如果 expected 输出需要调整，先更新 fixture 再改代码

#### 2.2 Java 实现

```
pine-java/src/main/java/page/liam/pine/operators/MyNewOperator.java
```

实现要点：
1. 实现 `Operator` 接口：`init(Map params)`, `process(Frame frame)`, `schema()`
2. 在 `AllOperators.ensureRegistered()` 中注册
3. 运行 `FixtureTest` 确认 operator fixture 通过
4. **不要调整 expected** — Java 必须匹配 Go 的输出

#### 2.3 其他语言

同 Java 的策略：实现接口 → 注册 → 跑 fixture → 对齐 Go 输出。

### Phase 3: 验证 — Cross-Validate

#### 3.1 单引擎 fixture test

每个引擎独立跑 fixture：

```bash
# Go
cd pine-go && go test ./... -run TestFixtures

# Java
cd pine-java && mvn test -Dtest=FixtureTest
```

#### 3.2 Cross-validate 验证

跑完整 cross-validate 确认所有引擎输出一致：

```bash
bash scripts/cross-validate.sh
```

新算子会被自动覆盖到以下段落：
- Section 3 (Execution): 如果有 pipeline fixture 引用了此算子
- Section 1 (Codegen): schema 导出对比

如果新增了 operator fixture，它会在各引擎的内部 fixture test 中被执行，但 cross-validate.sh 通过 pipeline fixture 间接验证。

#### 3.3 手动字节级对比（调试用）

```bash
# 构造一个只含新算子的最小 pipeline
cat > /tmp/test_config.json << 'EOF'
{
  "operators": {
    "op1": { "operator": "my_new_operator", "params": {...} }
  },
  "pipeline": ["op1"]
}
EOF

cat > /tmp/test_req.json << 'EOF'
{ "common": {...}, "items": [...] }
EOF

# 对比
diff <(pineapple-run -config /tmp/test_config.json -request /tmp/test_req.json) \
     <(java -cp ... page.liam.pine.RunCli -config /tmp/test_config.json -request /tmp/test_req.json)
```

## 常见跨引擎差异源

### 数值

| 场景 | 陷阱 | 对齐方式 |
|------|------|---------|
| JSON 整数 | Go: `float64`, Java: `Integer`/`Long` | 输出时保持原类型 |
| 除法 | Go: int/int=int, Java: int/int=int | 明确语义 |
| NaN/Infinity | Go: JSON 输出 null | 所有语言: 替换为 null |
| 浮点精度 | IEEE 754 | 避免精度敏感计算 |

### 排序

| 场景 | 陷阱 | 对齐方式 |
|------|------|---------|
| Map 序列化 | Go 按 key 字母排序 | 所有引擎必须对齐 |
| 稳定排序 | Go sort.SliceStable | 确保所有语言用 stable sort |
| Shuffle | 需要确定性种子 | 算子不做 shuffle 或用固定种子 |

### 字符串

| 场景 | 陷阱 | 对齐方式 |
|------|------|---------|
| HTML chars | Go 默认转义 `<>&` | 所有引擎需要实现 GoFormat |
| Unicode | U+2028/U+2029 转义 | 同上 |
| nil vs "" | Go nil string → JSON null | 区分空字符串和 null |

### 集合

| 场景 | 陷阱 | 对齐方式 |
|------|------|---------|
| 空 list | Go nil slice → `null` vs `[]` | 统一为 null 或 [] |
| 空 map | 同上 | 统一 |
| 字段缺失 | Go omitempty | 明确哪些字段省略 |

## Schema 注册检查清单

新算子必须在所有引擎中注册相同的 schema：

- [ ] 算子名称一致（小写蛇形: `my_new_operator`）
- [ ] 参数名称一致
- [ ] 参数类型一致 (string/int/float/bool/list/map)
- [ ] Required 标记一致
- [ ] Default 值一致（注意 int 0 vs float 0.0）
- [ ] metadata (common_input/item_input/common_output/item_output) 一致

验证：cross-validate Section 1 (codegen schema parity)。

## 错误处理

算子执行错误的处理也需要跨引擎一致：

```
// 当 fail_on_error=true 时
- 输入无效 → Engine 返回 error (HTTP 500, CLI exit 1)
- error message 格式: operator "op_name": <具体错误>

// 当 fail_on_error=false 时 (默认)
- 输入无效 → skip 当前算子，warnings 中记录
- warnings 格式: operator "op_name": <具体错误>
```

如果新算子有 `fail_on_error` 参数，需要在 `fixtures/errors/` 中添加对应的错误 fixture。

## 完成标准

- [ ] `fixtures/operators/<name>.json` 覆盖 golden path + edge cases
- [ ] Go 实现通过 fixture test
- [ ] Java 实现通过 fixture test (输出与 Go 一致)
- [ ] 其他已有引擎实现通过 fixture test
- [ ] Schema 在所有引擎中注册一致 (cross-validate Section 1 通过)
- [ ] Pipeline fixture 覆盖组合场景 (cross-validate Section 3 通过)
- [ ] Error fixture 覆盖错误路径 (cross-validate Section 5 通过)
- [ ] 全量 `cross-validate.sh` 绿色
