# 隔离窗口模式(Isolated Window Mode)

## 目标

一个 OBU session 的全部工作默认发生在一个**独立的、不抢焦点的后台 Chrome 窗口**里:
新标签页、claim 的标签页都落进这个窗口;任务结束无保留时自动关窗。
窗口**默认不最小化**(用户明确要求),`focused:false` + `active:false` + 靠角落的小尺寸定位
保证全程不打扰用户现有操作。**隔离是默认行为**:不使用用户已有的标签页,除非用户显式要求
(claim 是显式动作,`OBU_WINDOW_MODE=user` 是会话级显式退出)。

## 范围

- 包含:
  - **默认模式翻转**:`OBU_WINDOW_MODE` 语义改为——缺省即隔离;取值
    `user` 显式退出隔离(旧行为,新标签页进用户聚焦窗口)。`minimized` 本轮
    不实现(用户明确不要最小化)。
  - 扩展:session 级“拥有窗口”模型(get-or-create / 复用 / 失效重建 / finalize 关窗)
  - 扩展:claim 跨窗口时把标签页 move 进隔离窗口(维持 tab group 单窗口不变量)
  - 扩展:user_tabs() 排除属于 session 窗口的标签页(防止 agent 自 claim)
  - 扩展:finalize 时 deliverable 先移到用户可见窗口再关隔离窗(默认路径同样生效)
  - Go CLI:`--window-mode` flag + `OBU_WINDOW_MODE` env(经 applySessionDefaults 注入,覆盖 actionRunner 与 CLI 两条路径)
  - Python helper:`open_tab(url, window_mode=None)` 透传
  - JS SDK:createTab/newTab 透传可选 windowMode
  - 文档:SKILL.md(含“不主动 claim 用户 tab、除非用户要求”的指南翻转)、ARCHITECTURE.md
- 不包含:
  - 删除 claim/`user` 模式——显式要求时仍可用
  - 多 profile/browser 下的窗口策略差异(沿用现有 profile 路由)
  - deliverable/handoff 语义之外的窗口管理(不回收含残留用户 tab 的窗口)

## 背景

- 相关文档:`docs/HISTORY_GUIDE.md`、`docs/PLANS_GUIDE.md`、`docs/ARCHITECTURE.md`
- 相关代码路径:
  - `apps/chrome-extension/background.js`(`createTab`/`claimUserTab`/`getUserTabs`/`finalizeTabs`/`createBackgroundTab`/`SessionStore.getSession`)
  - `cmd/open-browser-use/main.go`(`newOpenTabCommand`/`applySessionDefaults`/`invokeWithProfile`)
  - `cmd/open-browser-use/script_helpers.py`(`open_tab`)
  - `packages/open-browser-use-js/src/index.ts`(`createTab`/`newTab`)
- 已知约束:
  - `chrome.tabGroups` 是窗口级的:同一 session 的 tab 必须同窗口(move 进隔离窗口是硬约束,不是偏好)
  - `chrome.windows` API 不需要 manifest 权限(官方文档确认),manifest 无需改动
  - `windows.create` 的 `left/top/width/height` 不能与 minimized/maximized/fullscreen 组合——我们不用 state,可以定位
  - macOS 上新窗口即使 `focused:false`,Dock 上 Chrome 可能短暂闪动(平台行为,不可消除)

## 风险

- 风险:claim 一个用户正在看的页面会把它从用户窗口“抽走”到隔离窗口(可见变化)。
  缓解:claim 改为显式动作,默认指南不再主动 claim;只有用户要求时才发生。
- 风险:隔离窗口残留被 release 的用户 tab 时,关窗会连带关掉用户 tab。
  缓解:finalize 只在窗口为空(`tabs.query` 计数 0)时才 `windows.remove`,残留用户 tab 的窗口保留。
- 风险:session.windowId 持久化在 chrome.storage.local,Chrome 重启/用户关窗后失效。
  缓解:`ensureSessionWindow` 每次都 `chrome.windows.get` 校验,失效即重建并回写。
- 风险:env `<->` 会话中途切换导致部分请求带 mode、部分不带,破坏单窗口不变量。
  缓解:隔离状态以 session.windowId 是否已存在为准("一旦隔离,永久隔离"),mode 参数只负责开启。

## 里程碑

1. 方案收敛(已完成:本计划)。
2. 扩展侧实现 + 单测(`isolated-window.test.mjs`)。
3. CLI/Python/SDK 参数透传 + 测试。
4. 文档(SKILL/ARCHITECTURE)+ 全量验证(`make ci`)+ 真浏览器冒烟。
5. history 记录。

## 验证方式

- 命令:`make ci`(go test、node --test apps/chrome-extension/*.test.mjs、pnpm 单测、python unittest、打包脚本)
- 新增单测:`node apps/chrome-extension/isolated-window.test.mjs`
  - createTab(隔离)首建窗口、二次复用同一 window id
  - claim 跨窗口 tab -> move 进隔离窗口并入组
  - user_tabs() 排除隔离窗口内 tab
  - finalize:deliverable 先移用户窗口;会话无保留且窗口空 -> windows.remove;handoff 保留窗口
  - 默认模式不触发 windows.create
- 手工检查(真 Chrome,`make local-install` 后):
  - `OBU_WINDOW_MODE=isolated obu` 连续 `open_tab()` 两次 -> 同一后台窗口、当前焦点不变
  - `user_tabs()` 不含工作tab;`finalize_tabs()` 后隔离窗口消失,用户窗口原样

## 进度记录

- [x] 确认范围和约束。
- [x] 扩展侧实现(窗口模型 + claim + user_tabs 排除 + finalize 关窗)。
- [x] CLI/Python/SDK 透传。
- [x] 单测 + `make ci` 全量通过。
- [x] 真浏览器冒烟验证(六项检查全绿,含 user 模式对照与零残留确认)。
- [x] SKILL/ARCHITECTURE 文档同步(history 记录随提交)。

## 决策记录

- 2026-08-26:隔离语义持久化放在扩展 session 记录(`windowId`),而非每次请求参数——
  一旦会话建立隔离窗口,后续请求即使丢了 env 也保持一致,杜绝跨窗口组分裂。
- 2026-08-26:不最小化(用户明确);用 `focused:false` + `active:false` + 底右下角小尺寸定位
  实现“不打扰”。macOS Dock 闪动属平台行为,接受并在文档标注。`minimized` 值本轮不实现,
  需要时加一行 `state:"minimized"` 即可。
- 2026-08-26:**隔离翻转默认**——`OBU_WINDOW_MODE` 缺省即 `isolated`;"不使用用户已有
  tab,除非用户要求"通过两层实现:技术上 claim_tab 始终是显式逐页动作(用户点名才发生),
  会话级显式退出用 `OBU_WINDOW_MODE=user`;指南上 SKILL.md 把“reuse 优先”改为“默认不 claim”。
- 2026-08-26:deliverable 在 finalize 先 move 到用户可见窗口再入交付组,避免交付物
  藏在后台窗口里;窗口仅在为空时才关闭,绝不连带关掉残留用户 tab。
- 2026-08-26:`windows.create` 自带一个初始 about:blank 标签页,不复用的话会残留为
  无主空白页、导致 finalize 永远关不掉窗口——因此建窗后把初始 tab 直接复用作会话
  第一个 tab(建窗时返回 created 标志,调用方复用 win.tabs[0])。
- 2026-08-26:**真机排障结论**——`chrome.tabs.group` 把新组钉在*当前(聚焦)窗口*
  并把 tab 拽过去,`createProperties.windowId` 在这台 Chrome 上被无视;探针实测
  (组落窗 + 归位反制)证实后,确定隔离会话**不建 tab group**,成员关系改用
  “归属窗口查询 + tabOrigins 并集”,tab group 仅保留给 user 模式与交付物。
- 2026-08-26:混合会话(隔离 session 内显式 `window_mode="user"`)的标签页
  不在会话窗口,窗口式查询会漏掉导致 finalize 残留——`getSessionTabs` 用
  `tabOrigins` 并集兜住,冒烟确认零残留。
- 2026-08-26:**外部查证**(用户要求核实他人反馈)——Chromium 主干源码
  `TabsGroupFunction::Run` 确认 `createProperties.windowId` 在主干被尊重,
  省缺时默认“当前窗口”,且“把 tab 移入目标窗口”是设计行为;本机显式传参
  仍被无视属于版本行为差异。无精确同款 issue(最接近:Claude Code
  #63934“新窗口建组报 Grouping is not supported”,ai-group-tabs #84
  “有时必须传 windowId”),均指向该 API 跨版本行为不一致。结论修正：建组仍需保留（可视化组织），但绕过
  该 API 的不可靠面——用“替身 tab 踩建组路径 + `tabGroups.move` 整组搬回 + 静默
  退化”的确定性方案，隔离窗口内恢复建组。- 2026-08-26:**归组次序坑(真机冒烟发现)**——替身 tab 若在真实 tab 归组之前被删,
  Chrome 会因空组自动解散而丢掉刚建立的组(观察到 `groupId: -1` 静默降级);
  因此“**先让真实 tab 归组、再删替身**”是硬约束。假件已模拟真实 Chrome
  (空组解散、对已解散组调用抛错)以锁住该回归;真机冒烟确认工作 tab
  共用一个真实正数 groupId、组锚定在隔离窗口内。
- 2026-08-26:**finalize 泄漏修复(外部 agent 实测触发)**——跨进程复用会话 tab 时 agent
  惯用 `claim_tab` 重新选中自己的工作页,但 `claimUserTab` 无条件把 origin 写成
  "user" → finalize 按用户页"释放"而非关闭,工作 tab 与隔离窗口残留(linux.do
  案例实锤)。修复:同会话已有 "agent" 记录时 claim 不再降级;且会话自有窗口内
  无 origin 记录的 tab 一律视为 agent(覆盖 claim 首建窗口的初始空白页),
  finalize 将其关闭。真机复验通过。
- 2026-08-26:**API 职责评审(用户确认)**——`claim_tab`/`claimUserTab` 兼任双重职责
  成立:① 接管用户标签页(契约本意);② 跨进程重选本会话的 agent 工作页
  (唯一设置 current tab 的通道)。两种用途共享同一个扩展 handler,origin-flip
  泄漏缺陷修复必须落在 handler 汇聚点(prelude / CLI claim-tab / action 模式
  三个入口全覆盖);纯用户 tab 的 claim 语义不变(无记录 → origin=user)。
  不引入独立 select_tab 助手,接受 API 的实用双重身份。
