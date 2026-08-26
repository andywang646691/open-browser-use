## [2026-08-26 01:46] | Task: 隔离窗口模式——OBU 默认在专属后台窗口工作

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> OBU 能新开浏览器窗口并在里面开标签页吗?……让 OBU 所有工作都在新窗口中完成,
> 不打扰用户。修改:① 默认不使用用户已有 tab,除非用户要求;② 默认使用隔离
> 窗口、非最小化模式。

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension/`、`cmd/open-browser-use/`、`packages/open-browser-use-js/`、skill/架构文档

**Key Actions:**

- **[扩展·窗口模型]**: `BrowserBackend` 新增 session 级"拥有窗口"模型——`createTab`
  默认(且 `window_mode` 缺省/`isolated`)走 `ensureSessionWindow`:用
  `chrome.windows.create({focused:false, type:"normal", url:"about:blank"})`
  建专属后台窗口(不最小化、右下角小尺寸定位),`session.windowId` 持久化在
  session state;窗口失效自动重建;建窗自带的初始 tab 直接复用为会话第一个
  tab,保证 finalize 时窗口能真正关空。
- **[扩展·claim 单窗口不变量]**: 隔离会话的成员关系以“归属窗口”追踪
  （`tabs.query({windowId})` + `tabOrigins`），并保留 tab group 可视化：
  真机验证发现 `chrome.tabs.group` 会把新组钉在*当前*（聚焦）窗口并把 tab
  拽过去、`createProperties.windowId` 在部分 Chrome 构建上被无视，因此建组
  用“替身 tab + `tabGroups.move` 修复 + 静默退化”的确定性方案（社区查证后
  `tabGroups.move` 跨窗口搬组为可靠能力），真实会话 tab 全程不离开隔离窗口。`claimUserTab` 把
  用户 tab `chrome.tabs.move` 进隔离窗口；`getUserTabs` 排除所有 session
  自有窗口内的 tab。
- **[扩展·finalize 收尾]**: 无保留时仅当隔离窗口已空才 `windows.remove`(绝不
  连带关闭残留用户 tab);`deliverable` 先 move 到用户可见窗口再进
  `✅ Open Browser Use` 分组;`handoff` 保留时窗口存活供下轮继续。
- **[CLI/Python/SDK 透传]**: `obu open-tab --window-mode` flag +
  `OBU_WINDOW_MODE` env(经 `applySessionDefaults` 注入,flag 优先);Python
  prelude 直连浏览器 socket,因此在 `script_helpers.py` 里自己读 env 注入
  `window_mode`,`open_tab(url, window_mode=...)` 支持显式覆盖;JS SDK
  `createTab/createTab newTab` 透传 `windowMode`。
- **[测试]**: 新增 `apps/chrome-extension/isolated-window.test.mjs`(8 用例:
  默认隔离/复用同一窗口/user 退出/claim 移窗/user_tabs 排除/finalize 关空窗/
  不关残留用户 tab 的窗口/deliverable 先转移/handoff 保窗);Go 单测
  `TestApplySessionDefaults*WindowMode`(env 注入 + 显式覆盖优先级);Python
  `open_tab` window_mode 透传用例;JS SDK createTab window_mode wire 用例。
  `claim-conflict.test.mjs` 显式传 `window_mode:"user"` 聚焦冲突语义。
- **[文档]**: SKILL.md(env 增加 `OBU_WINDOW_MODE`;"reuse 优先"翻转为
  "默认隔离窗口、不主动 claim");ARCHITECTURE.md(隔离窗口模型段落);
  exec plan 收尾。

### 🧠 Design Intent (Why)

隔离默认化后,"session 拥有一个窗口"成为唯一基元:新标签页和 claim 的标签页
都落进同一窗口,天然"不使用用户已有 tab"(除非显式 claim 或设
`OBU_WINDOW_MODE=user`)。不打扰靠 `focused:false`+`active:false`+非最小化
角落尺寸,不做任何 focus/restore 调用。真机排障发现两个 Chrome 行为坑:
① `windows.create` 自带的初始 about:blank 页若不复用会残留为无主标签页,导致
finalize 永远关不掉窗口——建窗时复用 `win.tabs[0]`;② `tabs.group` 的新组
默认落在*当前(聚焦)窗口*且会把 tab 拖过去(createProperties.windowId 不可靠),
因此隔离模式下放弃 tab group、改用"归属窗口"追踪会话成员,tab group 仅保留
给 user 模式与交付物分组。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/isolated-window.test.mjs`（新增）
- `apps/chrome-extension/claim-conflict.test.mjs`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/main_test.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `packages/open-browser-use-js/src/index.ts`
- `packages/open-browser-use-js/test/frame.test.mjs`
- `skills/open-browser-use/SKILL.md`
- `docs/ARCHITECTURE.md`
- `docs/exec-plans/active/2026-08-26-isolated-window-mode.md`（进度）