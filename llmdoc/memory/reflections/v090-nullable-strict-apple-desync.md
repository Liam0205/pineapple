# v0.9.0 Nullable→Strict 翻转后 Apple DSL 契约脱节反思

## Task

v0.9.0 将 InputFieldSpec 默认模式从 Strict 翻转为 Nullable，JSON 契约键从 `nullable_common`/`nullable_item` 反转为 `strict_common`/`strict_item`。pine-go（及其他三个运行时）全部完成迁移，但 Apple DSL 侧未同步。

## Expected vs Actual

- **预期**：模型翻转是完整的——Apple DSL 侧应同步引入 `strict_common`/`strict_item` 声明能力，并移除已废弃的 `nullable_common`/`nullable_item`。
- **实际**：
  1. `apple/base.py` 仍保留 `nullable_common`/`nullable_item` 字段（L25-26）。
  2. `apple/compiler.py` 仍 emit `"nullable_common"`/`"nullable_item"` 到 JSON（L118-121）。
  3. pine-go `config/types.go` 只读 `"strict_common"`/`"strict_item"`（L64-65），DSL 输出被静默忽略。
  4. Apple DSL **没有任何 `strict_common`/`strict_item` 相关代码**——下游无法通过 DSL 声明 Strict 模式，只能手写 JSON。
  5. `unique_name()` 的 semantic tuple（L60-61）仍包含 `nullable_common`/`nullable_item`，但这两个字段在运行时已无语义——hash 里参与计算的字段与实际生效的字段不一致。

## What Went Wrong

1. **跨层变更未做端到端检查**：pine-go 侧翻转完成后，没有验证 Apple DSL 编译产物在新运行时下的实际效果。JSON 作为解耦契约是优势，但也意味着一侧的键名变更不会导致另一侧编译失败——静默忽略使错误不可见。
2. **"默认 Nullable"的安全错觉**：因为翻转后默认即 Nullable，旧 DSL 输出在大多数场景不崩溃，掩盖了 Strict 能力完全丧失的事实。功能可用 ≠ 契约完整。
3. **cross-validate 未覆盖"声明→生效"路径**：现有的 cross-validate 验证的是四引擎在同一 JSON fixture 下输出一致，但没有验证"Apple DSL 声明 nullable/strict → 编译 JSON → 运行时实际生效"的端到端路径。

## Root Cause

模型翻转涉及两个方向的变更：
- **正向**（运行时侧）：键名 `nullable_*` → `strict_*`，默认语义翻转——已完成。
- **逆向**（声明侧）：DSL 字段名、编译输出、hash 输入同步翻转——**未执行**。

根因是把"四运行时对齐"视为完整交付，遗漏了"声明层 → 运行时"这条纵向契约链。llmdoc 中的 operator-contract 和 dag-engine 文档描述了三态模型本身，但没有描述"哪一层负责哪个键名"的映射关系。

## Missing Docs or Signals

1. **缺少"JSON 键名 → 各层映射"表格**：`llmdoc/reference/operator-contract.md` 描述了三态模型的语义，但没有列出每个 JSON 键在 Apple DSL / pine-go / pine-java / pine-python / pine-cpp 各层的字段名和生效状态。如果有这张表，翻转时一眼就能看出 Apple 侧还在用旧名。
2. **缺少"声明→生效"端到端校验**：`llmdoc/guides/cross-layer-validation.md` 描述了 JSON 边界类型枚举和 codegen 语义验证，但缺少"DSL 声明某能力 → 编译产出的 JSON 包含正确键 → 运行时实际读取并生效"这条完整路径的校验指导。
3. **conventions.md 中 InputFieldSpec 三态模型描述未指明各层键名**：只说了 Nullable/Strict/Defaulted 的语义，没说 JSON 层用什么键名、Apple 层用什么字段名。

## Promotion Candidates

1. **"JSON 契约键 ↔ 各层字段映射"表格** → 提升到 `reference/operator-contract.md`。每次涉及 JSON 键名变更时，必须同步检查此表中所有层。
2. **"声明→生效"端到端校验步骤** → 提升到 `guides/cross-layer-validation.md`。在跨层变更的 checklist 中加入：对于 DSL 新增/修改的声明字段，验证编译产物 JSON 包含正确键名且运行时实际读取。

## Follow-up

1. **P0 修复 Apple DSL**：
   - `apple/base.py`：将 `nullable_common`/`nullable_item` 替换为 `strict_common`/`strict_item`（或同时支持两者并发出 deprecation warning）。
   - `apple/compiler.py`：emit `"strict_common"`/`"strict_item"` 而非 `"nullable_common"`/`"nullable_item"`。
   - `unique_name()` semantic tuple：同步更新字段。
   - 确认 `apple/flow.py` 中的动态分发和 codegen 生成的 helper 是否需要暴露 strict 声明接口。
2. **补充 fixture**：新增一个 Apple DSL → compile → pine-go 执行的端到端 fixture，验证 `strict_common` 声明确实导致 nil 输入报错。
3. **更新 llmdoc**：在 operator-contract.md 中补充 JSON 键 ↔ 各层映射表。
