## [2026-08-29 02:00] | Task: 重构 OBU skill 主流程

### 🤖 Execution Context

- **Agent ID**: `pi-assistant`
- **Base Model**: `当前会话模型`
- **Runtime**: `pi / skill 审查与文档修改`

### 📥 User Query

> 根据 writing-great-skills 的审查建议修改 open-browser-use skill。

### 🛠 Changes Overview

**Scope:** Open Browser Use skill、site memory reference、site memory design doc。

**Key Actions:**

- **[Process-first structure]**: 将 skill 重构为 Initialize、Load context、Execute、Finish、Report 五个线性步骤。
- **[Completion criteria]**: 为每个步骤增加可检查的完成标准，降低 premature completion。
- **[Memory checkpoint]**: 将经验识别、持久化、写后验证和 tab 清理合并为统一 Finish 流程。
- **[Progressive disclosure]**: 将 memory 格式细节留在 `references/memory.md`，主 skill 只保留触发条件和完成标准。
- **[Path consistency]**: 明确 memory root、site rows、techniques 和 runbooks 的完整路径。

### 🧠 Design Intent (Why)

此前 Agent 可能完成任务并用 prose 描述困难，却跳过经验落盘。新的主流程把“持久化并验证”放在最终回复之前，并将其定义为不可跳过的完成条件；同时减少主 skill 中重复的格式细节，保持入口短而可执行。

### 📁 Files Modified

- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/memory.md`
- `docs/design-docs/site-memory.md`
- `docs/histories/2026-08/20260829-0200-process-first-skill.md`
