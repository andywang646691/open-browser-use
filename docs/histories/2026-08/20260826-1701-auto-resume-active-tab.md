## [2026-08-26 17:01] | Task: 自动认领 session 活动页，消除跨 run claim_tab 仪式

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> (承接 x.com 抓取会话复盘与 P1.4 分析)每轮 `obu <<'PY'` 都要 `tabs() → grep
> URL → claim_tab` 三行样板，漏写就报 "no current tab"，agent 在真实会话里连
> 挂 3 次。经分析：扩展侧 `sessionState.activeTabId` + `withLogicalActive`
> 已经持久化并自愈"session 逻辑活动页"，`tabs()` 也返回 `active` 标记；
> 只是 prelude 的 `cdp()/evaluate()` 只查进程内 `_CURRENT_TAB`，从不问扩展。
> 用户确认执行 P1.4。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use/script_helpers.py`(go:embed 进 obu 二进制)、
`script_helpers_test.py`、SKILL.md

**Key Actions:**

- **[自动认领]**: 新增 `_ensure_current_tab()` —— `cdp()/evaluate()` 在进程内
  `_CURRENT_TAB` 为空时，调 `tabs()` 取 session 的 `active: true` tab 并镜像
  到本地指针（恰好一个）；不调 `claimUserTab`，因为该 tab 来自 `getTabs`，
  claim 的繁重副作用（组员校验/move 进隔离窗/origin/持久化）全部已成立，
  只剩本地赋值。**只看 session tabs，绝不碰 `user_tabs()`**。
- **[报错语义]**: 空 session（finalize 已关完）仍报 "no current tab"，但消息
  附带原因（"this session has no tabs"）；异常多 active 时报列表、提示显式
  claim。报错从"忘记念咒语"变成"真的没活可干"。
- **[契约同步]**: `cdp()/evaluate()` docstring 从 "Requires a current tab:
  call open_tab()/claim_tab() first" 改为 "Resumes the session's last active
  tab automatically; explicit pick for a specific tab"；SKILL.md Process
  model 段同步翻转（reclaim 不再是每轮必修）。
- **[单测]**: 替换 `test_cdp_requires_current_tab` 为空 session 用例；新增
  3 例：`cdp` 自动认领 active tab、`evaluate` 自动认领、显式 `claim_tab`
  覆盖默认优先。全套 32 例通过，`go test ./...` 通过。

### 🧠 Design Intent (Why)

"当前工作页"的权威事实在扩展 store 里、且 `withLogicalActive` 已做完
"读持久值 → 悬空则自愈 → 重新持久化"三件事；prelude 缺的只是一个把答案
镜像回进程内指针的动作。把"查询+认领"从 agent 的 prompt 层下沉到运行时，
消掉每轮样板 + 一轮 RPC + 一个失败模式。自动认领只覆盖"继续上次工作页"
这一默认场景；URL 定向选择、接管用户 tab、空会话这三类明确交给显式
`claim_tab`/`open_tab`，硬规则"不 claim 用户 tab 除非被要求"不受影响。

### 📁 Files Modified

- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `skills/open-browser-use/SKILL.md`
- `docs/histories/2026-08/20260826-1701-auto-resume-active-tab.md`(本文件)