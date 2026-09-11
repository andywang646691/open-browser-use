## [2026-09-11 18:00] | Task: 解决标签页挂起导致队列阻塞与空壳任务分组遗留

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `Claude / Codex`
- **Runtime**: `pi`

### 📥 User Query

> 看看 herdr pane wH:p1 的抓取过程，这次抓取发现两个问题：
> 1. 标签页移动到了 OBU 完成 tab group, 原始 tab group 留下了一个 tab group.
> 2. claim 到闲置标签页。我认为这一项应该由 OBU 内部处理，不需要外部关注。
> 请分析这两个问题并解决。

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension/`, `cmd/open-browser-use/`, `skills/open-browser-use/`, `docs/`

**Key Actions:**

- **内部接管标签页 Readiness**：
  - `attach` 前通过 `chrome.tabs.get` 识别 `discarded` 状态，并在后台发起 `chrome.tabs.reload`，无需外部激活；
  - 调整唤醒顺序：先通过 CDP `Page.setWebLifecycleState({state: "active"})` 解冻 renderer，再执行 `Emulation.setFocusEmulationEnabled(true)`；
  - 设定 6s 内部超时与 `pendingReadiness` 状态隔离；迟到 attach 自动 detach，避免阻塞后续操作或泄露控制权；
  - 主 frame 导航重放唤醒复用有界异步流程，光标初始化增加 300ms 约束。
- **队列过期保护与幽灵空白页根除**：
  - CLI 与 Python prelude 自动在 JSON-RPC 请求参数中注入调用方绝对截止时间 `_deadlineUnixMs`；
  - 扩展在出队执行前严格执行 `wait <= min(queueTimeoutMs, remaining caller deadline)`（默认 queue TTL 为 2s），超期操作直接废弃，绝不在客户端超时前夕盲目启动，彻底消除迟到执行遗留未导航 `about:blank` 的根源；
  - Python `open_tab(url)` 与 Go CLI `open-tab --url` 增加失败保护：若后续 attach 或导航失败，自动清理刚刚创建的临时标签页。
- **空壳任务分组根除与 Contract 边界守恒**：
  - 根因消除依赖队列 TTL 丢弃 + `open_tab(url)` 失败自愈补偿 + 隔离窗口启动页立即清理；
  - 严格维护 `tab-ownership.md` 契约：`markDeliverable` 仅组织指定的单个成果页，不根据 URL 或分组推测扫荡其他 agent 或用户标签页；
  - `moveTabIntoSessionWindow` 在为隔离窗口建窗时立即清理启动初始 blank。
- **文档与 Agent Skill 同步**：
  - 更新 `SKILL.md` 和 `references/tabs.md`：明确闲置/冻结/丢弃标签页的 readiness 由 OBU 底层透明处理，Agent 不得添加唤醒仪式或手动激活提示；明确调研只读来源页只需 release，无需误调用 `mark_deliverable`。

### 🧠 Design Intent (Why)

原事件记录确立的核心机制是未决 attach/wake 阻塞全局队列 -> 客户端超时 -> 迟到执行 createTab 遗留孤儿 blank 页。浏览器自动化框架应将后台状态支持（如冻结解冻、丢弃重载）内聚在底层，并对排队操作设立严格独立的队列 TTL 与调用方截止时间约束，彻底避免副作用操作迟到执行。同时严格遵守 explicit ownership 契约，不在成果整理时根据 URL 猜测关闭会话其他合法标签页。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/background-wake.test.mjs`
- `apps/chrome-extension/isolated-window.test.mjs`
- `apps/chrome-extension/move-to-deliverables.test.mjs`
- `apps/chrome-extension/tab-readiness.test.mjs`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/main_test.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `cmd/open-browser-use/script_transport_test.py`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/design-docs/tab-ownership.md`
- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/tabs.md`
- `scripts/tab-readiness-smoke.py`
- `docs/exec-plans/completed/2026-09-11-tab-readiness-and-empty-group.md`
