## [2026-08-29 01:35] | Task: 强制经验沉淀 checkpoint

### 🤖 Execution Context

- **Agent ID**: `pi-assistant`
- **Base Model**: `当前会话模型`
- **Runtime**: `pi / 文档与 skill 审查`

### 📥 User Query

> 按照建议修改 skill，使 panel agent 不会只在最终回答中描述困难，而是实际沉淀经验。

### 🛠 Changes Overview

**Scope:** Open Browser Use skill、site memory 参考文档和设计文档。

**Key Actions:**

- **[Mandatory checkpoint]**: 在最终回复前强制检查失败、意外结构、解法和可复用流程。
- **[Persistence verification]**: 明确只有写入并重新读取 `site-memory.lines`、technique 或 runbook 才算沉淀完成。
- **[Lifecycle ordering]**: 明确 checkpoint 与 `finalize_tabs()` 的关系，清理后仍可读写 memory，但不得再次调用浏览器 helper。
- **[Documentation sync]**: 同步 `references/memory.md` 与 site-memory 设计文档。

### 🧠 Design Intent (Why)

自然语言总结困难不等于经验持久化。将 memory 写入和写后验证提升为最终回复前的明确 checkpoint，减少 Agent 忘记调用 `memory_put()` 或只生成 prose 总结的情况，同时保持浏览器清理顺序不变。

### 📁 Files Modified

- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/memory.md`
- `docs/design-docs/site-memory.md`
- `docs/histories/2026-08/20260829-0135-mandatory-memory-checkpoint.md`
