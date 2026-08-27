# session_inject —— MAIN world content script 路线探索（备份场景用）

## 目标

验证/实现"页面脚本运行前注入"的**平台原生路线**：`chrome.scripting.registerContentScripts`
+ `world: "MAIN"` + `runAt: "DOCUMENT_START"`，绕开已被实证否决的
`Page.addScriptToEvaluateOnNewDocument`（chrome.debugger 扁平会话在导航时丢失注册，
同 URL reload 都不存活）。此路线作为**备份能力**探索：不取代数据面方案（主动
GraphQL / 被动 Network 捕获），只验证 & 沉淀"注入"这条路的可行实现，以备未来
需要 DOM 级介入（预导航补丁、fetch 拦截、启动期钩子）的具体场景。

## 背景事实（本轮已实证）

- `Page.addScriptToEvaluateOnNewDocument` 经 chrome.debugger 通道跨导航失活（marker 实测）。
- 覆盖 `window.IntersectionObserver` 必须发生在**页面脚本创建 observer 之前**（闭包捕获），
  即 document_start + MAIN world；事后覆盖无效。
- `Emulation.*` 无页面可见性仿真；silent audio 不解 IO；win 遮挡→hidden→IO 停摆。
- manifest 已具备 `scripting` 权限 + `host_permissions: <all_urls>`，content_scripts 未声明。

## 关键设计决策

1. **静态模板文件**（MVP）：content script 的 `js` 字段必须是包内文件路径；扩展目录
   不可写；MAIN world 无 chrome API（异步通道：storage/postMessage 都赶不上
   document_start）。故 MVP 把"可见性 + IO 模拟"写成静态 MAIN world 模板
   `session-inject-main.js`，`matches` 指向 session tab 的 origin。任意源码注入
   留作后续（同样受异步通道限制，价值有限——核心补丁就是这些模板）。
2. **会话级生命周期**：注入随 session 注册、随 finalizeTabs 注销
   （`unregisterContentScripts`），`persistAcrossSessions: false`，绝不残留。
3. **作用域收窄**：`matches` = 会话 tab 当前 origin（`<origin>/*`），`allFrames: false`。
4. **安全边界**：向任意站点 MAIN world 注入 = 强能力；仅限"会话活动期间 + 目标
   origin + 可精确回收"，新增 SECURITY 交代。

## 范围

- 包含：`session-inject-main.js` 模板、`injectMainScript` handler、finalize 注销、
  prelude helper `session_inject(...)`、扩展单测（fake chrome.scripting）、exec plan+history。
- 不包含：任意源码注入通道（异步竞态）、静态注册进 manifest、真机 Chrome 验证
  （依赖用户 reload 扩展，单独向用户请求）。

## 验证方式

- 单测：注册参数（MAIN/DOCUMENT_START/matches=origin/id 会话级）、重复注册幂等、
  finalize 注销、无 tab 报错。全套既有测试保持绿。
- 真机验证配方（需用户同意 reload 扩展后执行）：open_tab x.com → session_inject →
  检查 `window.__obuMainInject` 与 DOM 装载行为变化。

## 进度

- 2026-08-26：worktree `feat/session-inject-main-world` 创建，基线全绿；plan 落地。
- 2026-08-26：实现完成——`session-inject-main.js`（MAIN world 可见性+IO 模板）、
  `injectMainScript` handler（会话级 id、matches=会话 tab origin、幂等重注册、
  finalize 注销）、prelude `session_inject(script)` helper、打包清单加入新文件、
  SECURITY 交代。单测：扩展 7 文件全绿（含新增 main-inject.test.mjs 4 用例）、
  prelude 33 例、go test 全绿。待办：真机验证（需用户 reload 扩展）。
- 2026-08-27：**真机验证通过（用户同意 reload 扩展）**。过程要点：扩展 unpacked
  目录在主仓库 apps/chrome-extension，验证时把分支两份文件同步过去、SW 空闲冷启
  后成功加载新代码（发现 `runAt` 枚举须为小写 `document_start`，chrome.scripting
  与 CDP 命名不同，已修正+单测同步）。结果：① `__obuMainInject` 标记出现——
  **MAIN-world document_start 注入确在页面脚本前执行**（本栈 addScript 路线从未
  做到）；② 窗口最小化时页面 `document.visibilityState` 读作 visible 且
  `hasFocus:false`——覆盖生效、无焦点偷取；③ IO 模拟让虚拟列表渲染出整个首屏
  批次结构（页高 797→9669），但 x.com 帖子数据依赖 app boot 后 GraphQL，而该
  profile 的 boot 被 Cloudflare 卡死（旧根因）→ 帖子数仍 4。结论：注入机制与
  API 覆盖路线成立，数据侧阻塞与注入无关，仍由数据面方案兜底。验证后主仓库
  扩展目录已还原回 HEAD。