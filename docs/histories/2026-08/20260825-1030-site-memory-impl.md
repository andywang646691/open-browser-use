## [2026-08-25 10:30] | Task: 落地 site memory 一期（OBU 经验沉淀）

### 🤖 Execution Context

- **Agent ID**: pi-assistant
- **Base Model**: 当前会话模型
- **Runtime**: pi / Go CLI + python3 heredoc

### 📥 User Query

> 如何让每一次 OBU 访问页面的经验沉淀下来、下一次 agent 可以复用，且不造成
> skill 爆炸？—— 最终要求：将推荐的完整方案整理成设计文档并实现，不引入任何
> `obu memory *` 命令层。

### 🛠 Changes Overview

**Scope:** skill（`skills/open-browser-use/`）、CLI heredoc prelude
（`cmd/open-browser-use/`）、机械校验脚本（`scripts/`）、设计文档。

**Key Actions:**

- 新增 `docs/design-docs/site-memory.md`：完整设计（skill 面锁 1、经验只长在
  数据层；原子行→technique→runbook 三层；两个机械化检索触发器；
  防爆炸硬规则）。
- 新增 `skills/open-browser-use/references/memory.md`：格式、路径、grep 写法、
  决策树、逐类型示例、`memory_put` 用法——给 LLM 的"开卷考"参考。
- `skills/open-browser-use/SKILL.md` 新增「Experience memory」一节（简短入口）。
- `cmd/open-browser-use/script_helpers.py` 新增 `memory_put(...)`（校验 kind、
  归并子域、禁用 `|`、append 安全）+ 注册进 `_help()`；`script_test.go` 新增
  3 个 Go 测试（格式/追加+归并/校验）。
- 新增 `scripts/obu-memory-check.sh`（机械校验：行格式、kind、临时 ref、密钥、
  索引预算、runbook header）。
- 设计文档状态改为"已实现"，§13 更新为落地清单。

### 🧠 Design Intent (Why)

- **不造命令层**：读取是 agent 原生能力（grep/find/read），任何 `memory *`
  命令都是重复抽象；只留一个 ~70 行 Python helper 负责写入序列化，贴合
  OBU「模型不手写 JSON/转义」的既有原则；且写格式可现查 `help(memory_put)`，
  不依赖 LLM 先验。
- **反爆炸靠结构**：单域单 key、唯一写入口、阈值/查重、stale 清理、索引预算
  ≤50 行——技能数永锁 1，增长全部落在有界数据里。
- **检索不靠猜词**：失败信号（≥2 次或静默失败）时无条件全量读 techniques
  索引，靠触发信号自匹配；关键词只作已知目标的快通道。
- 参考 ego-browser learnings 吸收「map, not diary」纪律与"能机检的约束
  变机械检查"，但不内嵌进 skill（避免门槛过高/skill 包膨胀）。

### 📁 Files Modified / Added

- `docs/design-docs/site-memory.md`（新增，设计定稿 + 落地状态）
- `docs/design-docs/index.md`（登记新设计文档）
- `skills/open-browser-use/references/memory.md`（新增）
- `skills/open-browser-use/SKILL.md`（加「Experience memory」一节）
- `cmd/open-browser-use/script_helpers.py`（加 `memory_put`）
- `cmd/open-browser-use/script_test.go`（加 3 个测试）
- `scripts/obu-memory-check.sh`（新增）
