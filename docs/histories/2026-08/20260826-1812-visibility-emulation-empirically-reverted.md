## [2026-08-26 18:12] | Task: 实证推翻可见性仿真，回滚无效代码并修正 SKILL

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 测试一下 `Emulation.setPageVisibilityOverride {visibilityState:"visible"}` 起作用没有。

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension/background.js`、`isolated-window.test.mjs`、`skills/open-browser-use/SKILL.md`

**Key Actions:**

- **[实测一·方法不存在]**: 在 Chrome 151 上手动调 `Emulation.setPageVisibilityOverride`
  → -32601 "wasn't found"。枚举 Emulation/Page 域候选方法：全部不存在可见性
  emulation 命令；对照 TOT protocol.json（curl 拉协议 spec）确认当前 CDP 的
  Emulation 域**没有任何页面可见性方法**（仅废弃的 `setVisibleSize`），Page 域
  只有 `setWebLifecycleState`（frozen/active，非可见性）。此前提交
  `f027529` 里"Chrome 129+ 可用"的说法是错的（该方法历史上也从未生效过）。
- **[实测二·行为矩阵]**（AppleScript 控制 OBU 自己的会话窗口 + evaluate 读取）：
  新建窗口在最前=visible；最小化=hidden；`Page.setWebLifecycleState("active")`
  无法翻转（仍 hidden）；还原窗口但被用户窗口遮挡=仍 hidden；唯一能翻转的是
  `Page.bringToFront`（→visible，且 `hasFocus:true`——**必然抢焦点**）。
  结论：visible/hidden 由**窗口遮挡**驱动，CDP 无仿真手段；隔离窗口一旦被
  用户窗口遮住，SPA 的可见性门控懒加载必然停摆，改窗口尺寸治标不治本。
- **[回滚]**: `attach()` 中新增的 emulation 块整体移除（在所有现代 Chrome 上
  静默失败，属于无效代码+虚假信心）；Test 13 与 fake 的 sendCommand 录制一并
  移除。六个扩展测试文件全部通过。
- **[SKILL 修正]**: 删除"OBU emulates page visibility on session tabs
  automatically (Chrome 129+)"的错误声明；改为诚实指引——隔离窗口被遮挡/最小
  化时 `document.visibilityState==="hidden"`，可见性门控站点滚动会停，
  **建议走 network-capture（`wait_for_event`）而非 DOM 滚动抓取**；只有确实
  需要页面真可见（bringToFront/用户窗口 tab/挪窗）时必须先询问用户。

### 🧠 Design Intent (Why)

上轮根据错误的记忆引入了不存在的 CDP 方法，属于"文档记忆当先验"的翻车。
这次用真机实测把机制钉死：可见性不受 CDP 控制，只受窗口遮挡驱动。由此
修正产品方向——OBU 不假装能仿真可见性，而是把"别依赖 DOM 滚动"（网络事件
捕获，已有的 `wait_for_event`）立为优先路线，把"必须真可见"的兜底动作标为
高打扰、需用户同意。约束（不打扰、先询问）不变量保持不变。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/isolated-window.test.mjs`
- `skills/open-browser-use/SKILL.md`
- `docs/histories/2026-08/20260826-1812-visibility-emulation-empirically-reverted.md`(本文件)