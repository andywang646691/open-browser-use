## [2026-09-14 16:45] | Task: 统一在任务完成时标记成果页

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `relay-cpa/gpt-5.6-luna`
- **Runtime**: `pi`

### 📥 User Query

> 修改 skill，任务完成后统一 mark_deliverable。

### 🛠 Changes Overview

**Scope:** `skills/open-browser-use/`

**Key Actions:**

- **强化收尾流程**：要求已验证的浏览器任务在 `release_tabs()` 前，对所有本身属于用户可见结果的已拥有标签页统一调用 `mark_deliverable(id)`。
- **保留来源页边界**：仅阅读的研究来源页不标记；没有成果页时明确记录 `no deliverable tab`。
- **同步参考文档**：更新标签页生命周期说明，避免 Agent 只释放、不整理成果页。

### 🧠 Design Intent (Why)

将成果整理从可选示例提升为任务完成时的统一收尾门槛，同时保留“来源页不等于成果页”的 ownership 与页面生命周期边界。

### 📁 Files Modified

- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/tabs.md`
- `docs/histories/20260914-1645-skill-mark-deliverable-wrapup.md`
