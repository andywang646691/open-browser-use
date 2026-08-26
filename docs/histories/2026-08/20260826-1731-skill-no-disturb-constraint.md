## [2026-08-26 17:31] | Task: SKILL.md 明确「不打扰用户」约束——优先无打扰方案，必要打扰先询问

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> (承接 x.com 回放复盘)回放证实可见性门控是 SPA 抓取头号阻塞，agent 靠
> `Page.bringToFront`（抢焦点）解决。用户要求：SKILL.md 中明确约束不打扰
> 用户——优先尝试其他办法，必须要打扰时先询问用户。

### 🛠 Changes Overview

**Scope:** `skills/open-browser-use/SKILL.md`(symlink 同步到 `~/.pi/agent/skills/`)

**Key Actions:**

- **[新条目「Don't disturb the user」]**: 在 "How to use it" 的 Tabs 之后新增
  一条行为准则——隔离窗口存在的目的就是全程不碰用户的屏幕/焦点/tab；优先
  无打扰的 CDP 替代方案，并给出具体先例：站点因页面不可见而不懒加载时，
  先试 `cdp("Emulation.setPageVisibilityOverride", visibilityState="visible")`
  （Chrome 129+，不抢焦点），再考虑 `Page.bringToFront`（抢焦点）；禁止为
  方便而用 `OBU_WINDOW_MODE="user"`。若唯一可行路径确实会打扰用户
  （抢焦点/挪窗/用户窗口开 tab），**必须先询问并获得明确同意**。
- **[Hard rules 新增]**: 「Never steal focus or visibly rearrange the user's
  windows/tabs」——与既有 hard rules 并列；先试无打扰替代、没有才询问。

### 🧠 Design Intent (Why)

x.com 回放中 agent 的逃生动作是 `Page.bringToFront`，它碰了用户焦点——之前
"不打扰"只写在隔离窗口的默认行为描述里，没有变成**显式决策约束**。这次把
约束升级为：默认值(隔离窗口) → 优先无打扰替代(Emulation 覆盖) → 必要时
询问用户。顺序即优先级，且给 agent 一个具体的无打扰逃生口
(`Emulation.setPageVisibilityOverride`)，避免它每次都落到抢焦点手段上。

### 📁 Files Modified

- `skills/open-browser-use/SKILL.md`
- `docs/histories/2026-08/20260826-1731-skill-no-disturb-constraint.md`(本文件)

## [2026-08-26 17:40] 修订：可见性模拟改由 OBU 在 attach 时自动处理

用户指出：SKILL 里写 CDP 调用说明是多余的——这个调用应该在每次 open
/claim tab 时由 OBU 自己处理。修订：

- **[background.js]**: `attach()`（session tab 的 debugger 汇聚点，createTab/
  claim/每次 evaluate/cdp 前都经它）在**每次新 attach** 后静默发送
  `Emulation.setPageVisibilityOverride {visibilityState:"visible"}`（Chrome
  129+；老版本 catch 掉、保持原 hidden 行为）。已 attached 的 tab 不重复
  发送。不抢焦点、用户无感知。
- **[单测]**: isolated-window.test.mjs 新增 Test 13——attach 后恰好一次
  emulation 命令、参数 visible、再次 attach（已 attached）不重复。fake
  chrome.debugger 增加 sendCommand 录制。全扩展测试通过。
- **[SKILL.md]**: 删除具体的 `cdp("Emulation.setPageVisibilityOverride")`
  调用说明，改为“OBU 自动模拟 session tab 可见性”；保留“必须打扰先询问”
  原则与 hard rule。