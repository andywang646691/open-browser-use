## [2026-08-25 11:34] | Task: 让 tab id 兼容数字字符串（claim_tab 兼容性）

### 🤖 Execution Context

- **Agent ID**: pi-assistant
- **Base Model**: 当前会话模型
- **Runtime**: pi / Node test + Go CLI + python3 heredoc

### 📥 User Query

> heredoc 里 `claim_tab("279925170")`（字符串 tab id）报
> `RuntimeError: obu claimUserTab: claimUserTab requires an integer tabId`；
> 用户问「应该提高点兼容性？」—— 让字符串数字 tab id 也能用。

### 🛠 Changes Overview

**Scope:** Chrome extension 后端 gate（`apps/chrome-extension/`）、CLI heredoc
prelude（`cmd/open-browser-use/`）、两处测试。

**Key Actions:**

- `background.js` `requireTabId`：数字字符串（`"42"`、`" 42 "`）自动转 int；
  非法的（`"abc"`、`true`、`42.5` 等）仍抛错，但错误信息带上实际收到的值
  （`got "nope"`）。这是唯一权威 gate，`claimUserTab`/`finalizeTabs`/`attach`/
  `detach`/`moveMouse`/clipboard/`requireSessionTab` 等全部经过它，一处修好
  所有调用方（SDK、CLI、heredoc）。
- `background.js` `executeCdp`：原来只用 `typeof tabId === "number"` 判断，
  字符串 tabId 会绕过 session 校验并拿到畸形 sendCommand；改为有 tabId 时也
  走 `requireTabId` 归一化，并写回 `target.tabId` 为 int。
- `script_helpers.py` 新增 `_coerce_tab_id(...)`：int 直通、数字字符串去空白转
  int、布尔/负数/0/非数字字符串提前抛友好错误（提示用 `user_tabs()` 列 id），
  不再把字符串一路带到扩展端才报错；`claim_tab`/`navigate` 使用，`_CURRENT_TAB`
  内部始终保持 int。
- 文档同步：`claim_tab`/`navigate` docstring 标注「int (or numeric string)」。

### 🧠 Design Intent (Why)

- 修复放在**协议两端的边界上**而不是某一端：扩展端是权威 gate，改它等于一次
  修复所有下游调用方；prelude 端提前归一化 + 友好报错，避免「模型把 id 当字符串
  抄进脚本」这类常见失败在扩展端变成晦涩错误。
- 只接受 `^\d+$`（可去空白），刻意拒绝 `"1e3"`/`"0x10"` 这类 JS 隐式数字转换，
  避免布尔值/浮点等被 Number() 悄悄吞成合法 tab id。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/require-tab-id.test.mjs`（新增）
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`