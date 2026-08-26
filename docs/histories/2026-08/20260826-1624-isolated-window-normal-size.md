## [2026-08-26 16:24] | Task: 隔离窗口改为正常尺寸(近全屏、居中)

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> (承接 x.com 抓取 15 篇帖子的会话复盘)复盘指出:隔离窗口是「右下角小尺寸」,
> 小视口会让 SPA 虚拟滚动/IntersectionObserver 只装配少量条目,是时间线
> 「永远只有 3-4 篇、滚动不追加」的疑凶之一。用户确认:**隔离窗口使用正常尺寸**。

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension/`(background.js + 单测)、`skills/open-browser-use/SKILL.md`、exec plan、history

**Key Actions:**

- **[窗口边界]**: `isolatedWindowBounds()` 从「1150x860、右下角定位」改为「正常尺寸」:
  优先铺满可用屏幕(`screen.availWidth/Height - 2x24px 边距`,不再被
  `ISOLATED_WINDOW_WIDTH/HEIGHT` 常量封顶),并**居中**摆放;`screen` 不可读时
  回退常量,常量从 1150x860 提升到 1280x900。保留 `focused:false` + 非最小化
  的「不抢焦点」不变量,越界兜底的 320/240 下限保留。
- **[注释]**: `ensureSessionWindow` 与头部常量注释同步为 normal-sized 语义,并
  说明动机(小角落窗会让 SPA 虚拟列表少装载内容)。
- **[单测]**: `isolated-window.test.mjs` 的 vm 上下文注入 `screen: {1920x1080}`,
  用例 1 断言窗口大小为 `1920-48 x 1080-48` 且 `left/top=24`(居中),把
  「近全屏 + 居中」锁进回归;其余 5 个扩展测试文件不受影响。
- **[文档]**: SKILL.md 的 "non-minimized, corner-sized" → "normal-sized
  (fills the screen)";exec plan 目标段与进度段同步更新。

### 🧠 Design Intent (Why)

小角落窗让 agent 的视口只有 ~757px 高、对应 X 首页只能挂载 4 条帖子,而 SPA
虚拟列表的装载量直接决定「滚动抓取」这条路能否少花轮次。近全屏 + 居中是
「普通窗口」的最自然形态,同时最大化渲染视口;不抢焦点/非最小化这两条
「不打扰用户」的硬约束原样保留,行为变化的只有尺寸与位置。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/isolated-window.test.mjs`
- `skills/open-browser-use/SKILL.md`
- `docs/exec-plans/active/2026-08-26-isolated-window-mode.md`
- `docs/histories/2026-08/20260826-1624-isolated-window-normal-size.md`(本文件)