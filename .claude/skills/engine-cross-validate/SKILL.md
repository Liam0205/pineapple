---
name: engine-cross-validate
description: 将新 pine-foo 引擎接入 fixtures 测试和 cross-validate 脚本，确保与已有实现行为一致。
---

# engine-cross-validate

指导如何将新构建的引擎接入已有的 fixture 测试框架和 cross-validate 验证系统。

## 前置条件

- 新引擎已实现 Engine.create() + execute() 基本流程
- 新引擎有 CLI 入口（RunCli, RenderDAGCli）
- 共享 fixtures 目录结构不变

## Fixture 测试体系

### 1. Operator Fixtures (`fixtures/operators/*.json`)

单算子级别的输入/输出对比测试。

**格式**:
```json
{
  "operator": "filter_condition",
  "cases": [
    {
      "name": "remove matching string items",
      "params": { "value": "offline" },
      "metadata": {
        "common_input": [], "item_input": ["status"],
        "common_output": [], "item_output": []
      },
      "input": { "common": {}, "items": [...] },
      "expected": { "common": {}, "items": [...] }
    }
  ]
}
```

**接入方式**: 在新引擎中编写 fixture test runner：

1. 扫描 `fixtures/operators/*.json`
2. 对每个 case: 构造单算子 pipeline config → Engine.create → execute(input) → assert output == expected
3. 比较时需 normalize 数值 (int/float 等价)

**参考实现**: `pine-java/src/test/java/page/liam/pine/FixtureTest.java`

### 2. Pipeline Fixtures (`fixtures/pipelines/*.json`)

多算子 pipeline 级别的端到端测试。

**格式**:
```json
{
  "config": {
    "operators": {...},
    "pipeline": [...],
    ...
  },
  "static_resources": { ... },
  "cases": [
    {
      "name": "case description",
      "request": { "common": {...}, "items": [...] },
      "expected": { "common": {...}, "items": [...] }
    }
  ]
}
```

**接入方式**: 同上，但直接使用 fixture 中的 config 构造 Engine。

### 3. Error Fixtures (`fixtures/errors/*.json`)

验证非法 config 能正确拒绝。

**格式**:
```json
{
  "config": { ... },
  "expected_error": "error message substring"
}
```

**接入方式**: Engine.create(config) 应抛出包含 expected_error 的异常。

## Cross-Validate 脚本接入

### 脚本位置

`scripts/cross-validate.sh` — 7 个验证段落：

| 段 | 验证内容 | CLI 依赖 |
|----|---------|----------|
| 1 | Codegen schema parity | `pineapple-codegen` / Codegen class |
| 2 | Render-DAG parity | `pineapple-dag` / RenderDAGCli |
| 3 | Execution parity | `pineapple-run` / RunCli |
| 4 | Column-store execution | 同上 (大数据集) |
| 5 | Error parity | 同上 (error fixtures) |
| 6 | Server HTTP parity | PineServer (HTTP) |
| 7 | Cancellation parity | RunCli + timeout |

### 接入步骤

#### Step 1: 提供 CLI 入口

新引擎需要暴露以下命令行工具：

```bash
# 执行 pipeline
pine-foo run -config <path> -request <path> [-static-resources <path>]
# 输出: pretty-print JSON to stdout, errors to stderr + exit(1)

# 渲染 DAG
pine-foo dag -config <path> -format dot|mermaid [-collapse N]
# 输出: dot/mermaid 文本 to stdout

# 导出 schema
pine-foo codegen --export-schema <output-path>
# 或: pine-foo codegen -schema-json <output-path>
```

#### Step 2: 修改 cross-validate.sh

在 Pre-build 段添加新引擎的构建：

```bash
echo "    Building Foo engine..."
cd "$REPO_ROOT/pine-foo"
# 语言相关的构建命令
foo_build_command

# 定义执行函数
foo_run() {
  # 执行新引擎 CLI 的命令
  pine-foo-binary "$@"
}
```

在每个验证段中添加新引擎的对比（建议逐段接入）：

```bash
# 在 section 3 (execution parity) 中追加:
foo_result=$(foo_run run -config "$config_file" -request "$req_file" "${res_args[@]}" 2>/dev/null)
foo_norm=$(echo "$foo_result" | normalize_json)

if [[ "$go_norm" == "$foo_norm" ]]; then
  exec_pass=$((exec_pass + 1))
else
  fail "execution divergence (Go vs Foo): $fname case $i"
fi
```

#### Step 3: 逐段接入顺序

推荐接入顺序（按依赖和调试难度）：

1. **Section 3 (Execution)** — 最核心，验证引擎语义
2. **Section 5 (Error)** — 验证错误处理
3. **Section 2 (DAG)** — 验证 DAG 渲染
4. **Section 4 (Column-store)** — 大数据集压力测试
5. **Section 6 (Server HTTP)** — 需要启动 HTTP server
6. **Section 1 (Codegen)** — 需要实现 schema 导出
7. **Section 7 (Cancellation)** — 需要实现超时取消

### Step 4: 处理常见差异

#### JSON 输出 normalize

cross-validate 中有 `normalize_json` 函数处理 int/float 差异：

```python
def normalize(obj):
    if isinstance(obj, (int, float)):
        return float(obj)
    return obj
```

如果新引擎的数值表示和 Go 不同（如 `83` vs `83.0`），确保 normalize 能覆盖。

#### 字节级 vs 语义级对比

- Section 1 (codegen), 2 (DAG): **字节级** — `diff` 直接比较
- Section 3, 4 (execution): **语义级** — 经过 normalize_json
- Section 6 (server): **字节级** — HTTP response body 直接比较

新引擎要通过字节级段落，必须处理 JSON wire format 差异（见 `new-engine` skill）。

## 调试技巧

### 定位单个失败 case

```bash
# 手动运行单个 fixture
go_out=$(/path/to/pineapple-run -config config.json -request req.json)
foo_out=$(pine-foo run -config config.json -request req.json)
diff <(echo "$go_out") <(echo "$foo_out")
```

### 分层排查

1. **数值差异**: normalize 后对比 → 如果 normalize 后一致说明只是 int/float 表示差异
2. **Key 排序差异**: `python3 -c "import json; ..."` sort_keys 后对比
3. **Wire format 差异**: hexdump 比较确认转义字符差异
4. **逻辑差异**: 对 expected 做三方对比 (expected vs go vs foo)

### 增量验证

先跑单段：

```bash
# 只跑 execution parity
bash -c 'source scripts/cross-validate.sh' 2>&1 | grep -A5 "Execution parity"
```

或临时注释掉其他段落，聚焦调试。

## 完成标准

- [ ] 新引擎内部 fixture tests 全部通过 (operator + pipeline + error)
- [ ] cross-validate.sh 扩展为支持新引擎
- [ ] 所有 7 段验证对 Go vs Foo 全绿
- [ ] CI 中新引擎的测试和 cross-validate 已集成
