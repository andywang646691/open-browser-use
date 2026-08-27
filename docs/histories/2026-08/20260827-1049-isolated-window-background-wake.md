## [2026-08-27 10:49] | Task: 隔离窗口后台节流唤醒——CDP 层自动唤醒内置到 OBU

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 下面的 agent 的执行过程，也许我们可以将后台节流唤醒内置到 OBU 中：x.com 在隔离后台窗口里
> 懒加载停摆（`visibilityState==="hidden"`，滚动到 9925px 一个 HomeTimeline 请求都没有），
> 真机组合三层后恢复（文章数 4→11）：① `session_inject("visibility")` DOM 注入；
> ② `Emulation.setFocusEmulationEnabled(true)`；③ `Page.setWebLifecycleState({state:"active"})`。
> 其中③是胜负手——真正卡住懒加载的是 compositor 对 occluded 窗口的节流，DOM 注入无法覆盖。

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension/background.js`、`background-wake.test.mjs`（新增）、
`skills/open-browser-use/SKILL.md`、`docs/ARCHITECTURE.md`、`docs/SECURITY.md`、
exec plan（新增）+ history（本文件）

**Key Actions:**

- **[决策·interview]** 内置程度经用户拍板选 B：隔离窗口 CDP 层自动唤醒（focus 仿真 +
  lifecycle active）+ `session_inject` DOM 层保持显式。用户明确：**本轮不扩展 DOM 注入**，
  先真机验证 focus + lifecycle 单独是否足够。
- **[扩展·attach 唤醒]** `attach()` 对"首次 attach 且 tab 位于会话自有窗口"的 tab 自动
  应用两条 CDP 命令：`Emulation.setFocusEmulationEnabled({enabled:true})` +
  `Page.setWebLifecycleState({state:"active"})`。触发条件是 `session.windowId` 存在
  （隔离会话）且 `tab.windowId === session.windowId`（混合会话里落在用户窗口的 tab 不唤醒）。
  best-effort、逐命令容错（单个失败不阻断 attach），attach 结果透出 `wake` 状态便于观测。
- **[扩展·导航重放]** `handleCdpEvent` 增加 `Page.frameNavigated`（仅主 frame）分支：
  对已唤醒 tab 重放两条命令——导航会把 lifecycle 重置回 hidden+节流。
- **[扩展·生命周期]** `wokenTabIds` 集合按 tabId 追踪（导航事件只有 tabId 没有
  sessionId，无法反查）；attach 时加入、`chrome.debugger` detach 时清除；已 attach 的
  重复 attach 不重放（避免每次 evaluate 都多两条命令）。detach 后 focus 仿真随 debugger
  会话自动复位，无需额外清理。
- **[单测·新增]** `background-wake.test.mjs` 6 用例（fake debugger 录制 sendCommand +
  onDetach 触发）：隔离 attach 发两条命令 / 重复 attach 不重发 / user 模式不唤醒 /
  主 frame 导航重放、子 frame 不重放、detach 后静默、再 attach 再唤醒 / 命令失败容错 /
  落用户窗口的会话 tab 不唤醒。踩坑：vm realm 原型差异导致 deepEqual 误判（main-inject
  历史同款），改字段级断言。
- **[文档]** SKILL："无 CDP 可见性仿真"改为诚实三层叙述——自动 CDP 唤醒解除渲染器节流、
  `session_inject("visibility")` 补可见性读取门控站点、网络捕获仍是数据面首选；
  ARCHITECTURE 隔离窗口模型段落补充唤醒机制（含导航重放与 detach 复位）；
  SECURITY 新增"隔离窗口后台唤醒"边界（仅隔离会话 tab、user 模式不生效、不抢焦点、
  随 detach 复位、不触碰用户数据）。
- 全套通过：`make ci`（go test、扩展 8 个测试文件、prelude、SDK、打包、文档检查）。

### 🧠 Design Intent (Why)

8/26 回滚是"想伪造可见性本身"（不存在的 `setPageVisibilityOverride`）；用户 8/27 的实证
揭示真相：懒加载停摆是**渲染器层节流**问题，不是可见性读取问题——DOM 注入只能骗过页面
JS 读取，compositor 对 occluded 窗口的节流（rAF + IO 挂起）只有 CDP 层能唤醒。本轮的
产品方向因此翻转：OBU 不再"假装能仿真可见性"，而是让隔离窗口**真实地像可见窗口一样被
唤醒**——不抢焦点、detach 即复位、user 模式不生效。`session_inject` 保持显式（DOM 层
伪造是重行为变化，且用户要先验证 focus+lifecycle 单独是否足够，不预设结论）。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/background-wake.test.mjs`（新增）
- `skills/open-browser-use/SKILL.md`
- `docs/ARCHITECTURE.md`
- `docs/SECURITY.md`
- `docs/exec-plans/active/2026-08-27-isolated-window-background-wake.md`（新增）

### ⏭ 真机冒烟结果（2026-08-27 完成，用户配合 reload 扩展）

同机同 Chrome、同隔离会话条件，A/B 对照（均首次 attach 后立即读数）：

| 维度 | 无唤醒 baseline | 唤醒开启 |
|---|---|---|
| `document.hasFocus()` | false | **true** |
| `document.visibilityState` | hidden | **visible** |
| 滚动后文章数 | 4（卡死） | **4→11，持续增长**（pageH 25962→62990） |
| HomeTimeline 请求 | 从未命中（8s 超时） | **命中** |

用户原始故障精确复现（hidden + 卡 4 篇 + 无 HomeTimeline + scrollY/pageH 冻结），
唤醒后彻底恢复。**实证结论：focus 仿真 + lifecycle active 组合单独就够**——不仅解除
渲染器节流，还把被遮挡页面的读取翻为 visible（`visibilityState`→visible、
`hasFocus`→true），可见性门控站点（x.com）不再需要 `session_inject` 的 DOM 假
可见性。SKILL/ARCHITECTURE/SECURITY 已按此更正（早期表述"需 DOM 注入配合"是
基于 8/26 只单独测 lifecycle 的错误推断；组合行为此前未验证）。

未验证边界：最小化窗口（8/26 矩阵显示 lifecycle 单独无法翻转；组合对最小化窗口
的行为待验证，`session_inject` 保留为该边界的显式回退）。

验证过程无遗留：4 个冒烟 session 全部 finalize（tab 关闭、隔离窗口清空），主仓库
扩展目录已还原 HEAD（background.js + popup.js 临时钩子已移除）。