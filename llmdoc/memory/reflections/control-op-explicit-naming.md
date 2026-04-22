# 控制算子显式命名复盘

## Task
将 Apple DSL 控制算子从自动命名 (`transform_by_lua_{hash6}`) 改为显式命名 (`if_1`, `elseif_2`, `else_3`)，提升 DAG 可视化可读性。

## What Changed
- `apple/control.py` 的 `make_control_op` 新增 `name=f"{branch.kind}_{branch.ctrl_index}"`
- `type_name` 保持 `transform_by_lua` 不变，Go 引擎调度无感知
- 改动量：1 行代码

## What Went Well
- 改动极小且定向，利用了已有的 `OpCall.name` 显式命名机制，无需新增任何抽象
- `ctrl_index` 是全局唯一计数器，天然避免命名冲突
- 60 个现有测试全部通过，无回归

## Lessons
- 控制算子的可读性问题本应在最初设计控制流降级时就考虑——当时只关注了 `type_name` 在引擎调度中的作用，忽略了 `unique_name()` 在 DAG 可视化中的呈现效果
- `OpCall.name` 显式命名机制在此场景下非常有用，未来若有其他编译器生成的算子（如 merge 控制算子）也应考虑显式命名

## Doc Impact
- `llmdoc/architecture/apple-compiler.md` 控制流降级章节应反映显式命名
- `design_doc/05_operator_types.md` 和 `design_doc/06_json_config.md` 已同步更新
