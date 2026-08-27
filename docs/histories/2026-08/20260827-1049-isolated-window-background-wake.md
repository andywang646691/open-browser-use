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

## [2026-08-27 12:0X] | 续：user 窗口模式验证 + `session_inject` 退役删除

### 📥 User Query（续）

> 回到无隔离窗口模式，在用户窗口开 tab group，测试唤醒是否有效；如果 focus +
> lifecycle 有效，就删掉 session_inject。

### 🛠 Changes Overview

**Scope（续）：** `apps/chrome-extension/background.js`、`background-wake.test.mjs`、
`session-inject-main.js`/`main-inject.test.mjs`（删除）、`script_helpers.py(_test)`、
`scripts/package-chrome-extension.sh`、SKILL/ARCHITECTURE/SECURITY、exec plan + history

**Key Actions:**

- **[推广唤醒范围]** 唤醒触发从"仅隔离窗口（`session.windowId` + 归属窗口校验）"
  推广为**所有会话 tab**——user 模式（共享用户窗口）的后台 tab 同样 hidden+节流，
  同样需要唤醒。`attach()` 去掉隔离判断，attach 即唤醒；`wokenTabIds` 语义改为
  "会话 tab"维度。
- **[单测翻转]** Test 3（user 模式不唤醒 → 应唤醒）与 Test 6（非归属窗口 tab
  不唤醒 → 应唤醒）断言翻转，两用例全绿。
- **[user 模式真机验证]** `OBU_WINDOW_MODE=user` 会话，tab 落在用户 Chrome 窗口
  （Task - OBU 组），非选中后台 tab：唤醒后 `hasFocus=true`、
  `visibilityState=visible`、HomeTimeline 命中、文章 6→10→11 持续增长
  （pageH 40972→65319）。**focus + lifecycle 在两种模式都单独有效。**
- **[删除 `session_inject`]** 用户拍板：DOM 注入层整体退役——`injectMainScript`
  handler、`session-inject-main.js` MAIN-world 模板、`main-inject.test.mjs`、
  prelude `session_inject()` helper + 测试、打包清单条目、SKILL/ARCHITECTURE/SECURITY
  引用全部清除。`make ci` 全绿。

### 🧠 Design Intent

唤醒实证闭环后（遮挡窗口 + 用户窗口后台 tab 均恢复懒加载），MAIN-world DOM 注入
失去了存在意义：它唯一的价值是伪造可见性读取，而 CDP 组合已能同时伪造
`visibilityState`/`hasFocus` 并解除渲染器节流。删掉它减少了攻击面（少一类
page-visible 篡改能力）、减少维护面。最小化窗口边界未验证、删除后无回退手段，
风险接受（隔离窗口默认不最小化；user 模式不涉及最小化）。

### ⏭ 隔离窗口真机 A/B（2026-08-27，用户配合 reload 扩展）

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
的行为待验证——该边界随 `session_inject` 删除已无回退手段，见上文风险接受）。

验证过程无遗留：冒烟 session 均已 finalize（tab 关闭、窗口清空），主仓库在每一轮
验证后还原 HEAD（background.js + popup.js 临时钩子已移除）。

## [2026-08-27 12:2X] | 续：默认窗口模式翻转 + 合并入 main

### 📥 User Query（续）

> 默认使用用户窗口，然后合并到 main。

### 🛠 Changes Overview

**Scope（续）：** `background.js`（`sessionUsesIsolatedWindow` 默认翻转）、
`main.go`（flag 帮助文本）、`script_helpers.py`（注释/docstring）、
`isolated-window.test.mjs`（Test 1 默认=user，隔离用例显式化）、
SKILL.md/ARCHITECTURE.md（默认描述翻转）、exec plan + history

**Key Actions:**

- **[默认翻转]** 缺省/`user` = 用户聚焦窗口（默认，共享窗口模式），`isolated` 改为
  显式 opt-in（`OBU_WINDOW_MODE=isolated` / `window_mode="isolated"`）。隔离窗口
  机制完整保留，“窗口已建则永久隔离”不变量不变。
- **[单测]** Test 1 重写：默认模式不建窗口、tab 落用户窗口；显式 isolated 建
  专用窗口（尺寸/居中/组锚定断言保留）。隔离场景用例（claim/deliverable/
  handoff/repair/reclaim/claimfirst）全部显式传 `window_mode:"isolated"`。
- **[入库]** 分支 `feat/background-wake` 合并回 `main`（fast-forward），worktree
  拆除。

### 🧠 Design Intent

唤醒在两种模式均已真机实证有效后，用户窗口成为默认：模型更简单（tab 就在用户
浏览器里、无需理解“后台窗口”概念），且不牺牲懒加载/可见性门控站点的可驱动性。
隔离窗口作为不打扰用户的可选模式保留（显式 opt-in）。