# Pine-Java Schema 独立化设计

## 目标

使 Pine-Java 成为独立的、自含的 Schema 来源，与 Pine-Go 对等。两者可在 Schema / Config / Execution 三个层级进行交叉验证。

## 现状

```
Pine-Go Registry ──export──→ schema.json ──→ Pine-Java Codegen ──→ Python DSL
                                              (Java 是消费者)
```

Pine-Java Registry 当前仅存储 `(name, type, factory)`，没有参数规格、描述、校验能力。

## 目标架构

```
Pine-Go Registry  ──export──→ schema-go.json   ─┐
                                                 ├─ CI: schema-level cross-validate
Pine-Java Registry ──export──→ schema-java.json ─┘

Pine-Go Codegen   ←── 自身 Registry
Pine-Java Codegen ←── 自身 Registry (或外部 JSON，兼容两种模式)
```

## 设计细节

### 1. ParamSpec 类型

```java
public class ParamSpec {
    public final String type;        // "string", "int64", "float64", "bool", "any"
    public final boolean required;
    public final Object defaultValue; // null 表示无默认值
    public final String description;
}
```

与 Go `types.ParamSpec` 一一对应。

### 2. OperatorSchema 类型

```java
public class OperatorSchema {
    public final String name;
    public final OperatorType type;
    public final String description;
    public final Map<String, ParamSpec> params;
}
```

### 3. Registry 注册接口变更

```java
// 新签名 — 带 Schema 注册
public static void register(OperatorSchema schema, Supplier<Operator> factory)

// 注册时强校验（与 Go 一致）：
//   - name 非空
//   - type 合法
//   - description 非空
//   - 每个 param 的 description 非空
//   - 重复 name → PineErrors.RegistryError
```

向后兼容：保留旧的 `register(name, type, factory)` 但标记 @Deprecated，内部构造空 Schema。

### 4. ValidateAndExtractParams

```java
public static Map<String, Object> validateAndExtractParams(
    OperatorSchema schema, Map<String, Object> rawParams) throws PineErrors.RegistryError
```

逻辑（与 Go `registry.ValidateAndExtractParams` 完全对齐）：

1. 过滤保留键（`type_name`, `$metadata`, `skip`, `recall`, `sources`, `debug`, `row_dependency`, `common_defaults`, `item_defaults`, `for_branch_control`, `data_parallel`）
2. 遍历 schema.params：缺失 + required → error；缺失 + 有 default → 注入
3. 遍历剩余 params：未在 schema.params 中声明 → error
4. 返回清洁后的业务参数 map

### 5. BuildOperator 变更

```java
public static Operator buildOperator(String typeName, Map<String, Object> rawParams) throws Exception {
    OperatorEntry entry = operators.get(typeName);
    if (entry == null) throw RegistryError("unknown operator type");

    Map<String, Object> params = validateAndExtractParams(entry.schema, rawParams);
    Operator op = entry.factory.get();
    op.init(params);
    return op;
}
```

### 6. Schema 导出

```java
public static List<OperatorSchema> all();
public static String exportSchemaJSON();
```

输出格式与 Go `ExportSchemaJSON` 一致（JSON array of schema objects），确保可 diff。

### 7. Codegen 双模式

`Codegen.java` 支持两种数据源：

- `--schema-from-registry`：从自身 Registry 获取 Schema（独立模式）
- `--schema-json <path>`：从外部 JSON 文件读取（兼容模式，保留现有行为）

### 8. 算子注册改造

以 `TransformCopy` 为例，当前：

```java
Registry.register("transform_copy", OperatorType.TRANSFORM, TransformCopy::new);
```

改为：

```java
Registry.register(new OperatorSchema(
    "transform_copy",
    OperatorType.TRANSFORM,
    "Copies field values between common and item dimensions.",
    Map.of("direction", new ParamSpec("string", true, null,
        "Copy direction: common_to_item, item_to_common, common_to_common, or item_to_item."))
), TransformCopy::new);
```

### 9. ResourceSchema（对称设计）

```java
public class ResourceSchema {
    public final String name;
    public final String description;
    public final int defaultInterval;
    public final Map<String, ParamSpec> params;
}
```

ResourceManager 的 factory 注册也补充 Schema。

## 交叉验证策略

### Schema 层

CI 新增 step：

```yaml
- name: Cross-validate schemas
  run: |
    go run ./cmd/pineapple-codegen -schema-json schema-go.json
    cd pine-java && mvn -q exec:java -Dexec.mainClass="page.liam.pine.Codegen" -Dexec.args="--export-schema schema-java.json"
    diff <(jq -S . schema-go.json) <(jq -S . pine-java/schema-java.json)
```

### Config 层

已有：共享 `testdata/` fixture，两侧 load + validate。

### Execution 层

已有：cross-validation job 对比同一 fixture 的执行结果。

## 实施顺序

1. 新增 `ParamSpec.java` + `OperatorSchema.java`
2. 改造 `Registry.java`（新签名 + `validateAndExtractParams` + `exportSchemaJSON`）
3. 逐算子补充 Schema（18 个算子 + AllOperators.java 改造）
4. Engine.createInternal 中 `buildOperator` 自动走 validate 路径
5. Codegen 增加 `--export-schema` 和 `--schema-from-registry` 模式
6. ResourceSchema 补齐（可选，后续 PR）
7. CI 增加 schema diff gate

## 不变量

- Java 包名 `page.liam.pine` 不变
- JSON 配置格式不变（Schema 是引擎内部概念，不影响外部契约）
- 算子 `init(Map<String,Object> params)` 签名不变（但参数已被 validate 过滤）
- 现有 70 个测试的行为不变
