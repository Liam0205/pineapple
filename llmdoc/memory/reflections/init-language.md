# [llmdoc init 语言对齐反思]

## Task
- 复盘 Pineapple 的 llmdoc 初始化流程，记录为什么初版文档全部用英文生成，随后又被整体重写为中文。

## Expected vs Actual
- Expected outcome.
  - 初始化阶段应先识别项目与用户默认语言，并直接用中文生成 llmdoc 文档。
- Actual outcome.
  - init 流程产出了完整的调查结果和稳定文档，但语言未对齐，7 份文档全部先写成英文，随后因项目实际语言环境为中文而整批重写。

## What Went Wrong
- 只关注了文档结构完整性与内容覆盖度，没有把“目标语言”当作 init 的第一层约束。
- 没有读取并落实用户全局 `CLAUDE.md` 中的“Always answer in 简体中文”。
- 没有利用项目已有信号（如中文 `README.md`）来校验文档输出语言。

## Root Cause
- llmdoc init 流程缺少“语言探测与对齐”检查点，导致稳定文档生成前未确认项目文档语言与用户偏好语言是否一致。

## Missing Docs or Signals
- memory only: 需要记住 init 不只要判断技术边界，还要先判断输出语言。
- promote later: 在 llmdoc init/stable-doc 生成规范中加入明确步骤：先检查全局指令、README、现有文档语言，再决定输出语言；若信号冲突，先向用户确认。

## Promotion Candidates
- `must/` 或 llmdoc init 规范中增加“文档语言优先级”规则：用户明确指令 > 项目现有主语言 > 默认语言。
- `guides/` 中增加初始化检查清单：生成稳定文档前先做语言对齐检查。

## Follow-up
- 后续执行 llmdoc init 或大批量文档生成时，先读取全局/项目指令与 README 语言，再开始写文档；若未确认语言，不进入批量生成阶段。
