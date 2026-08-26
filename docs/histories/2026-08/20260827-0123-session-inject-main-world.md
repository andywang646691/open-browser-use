## [2026-08-27 01:23] | Task: session_inject —— MAIN world content script 路线探索（备份场景）

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 切分支实现方案：`Page.addScriptToEvaluateOnNewDocument`（chrome.debugger 通道）
> 已实证跨导航失活，探索平台原生替代——`chrome.scripting.registerContentScripts`
> 的 `world: "MAIN"` + `runAt: "DOCUMENT_START"`，作为其他应用场景的备份能力。
> 工作区：git worktree `.worktrees/session-inject-main-world`（分支
> `feat/session-inject-main-world`），与主线隔离。

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension/`（新 MAIN world 模板 + handler + finalize 注销）、
`cmd/open-browser-use/script_helpers.py`（新 helper）、打包脚本、SECURITY、
exec plan + history

**Key Actions:**

- **[模板 `session-inject-main.js`]**: MAIN world 静态模板（MVP = "visibility"）：
  覆盖 `Document.prototype.visibilityState` → "visible"，并用"observe 立即回填
  isIntersecting:true"的假 `IntersectionObserver`，让 IO/可见性门控的懒加载在
  被遮挡/最小化的会话窗口里照常工作，不抢焦点、免疫页面 CSP。content script 的
  `js` 必须是包内文件路径 + 扩展目录不可写 + MAIN world 无 chrome API，故动态
  任意源码留作后续（异步通道竞态，价值有限）。
- **[handler `injectMainScript`]**: 会话级注册——`matches` 由会话 tab 当前
  HTTP(S) origin 推导、`id` 会话级、`allFrames:false`、`persistAcrossSessions:
  false`；重复调用先 unregister 再 register（幂等、导航后自动重新定向新
  origin）；无 HTTP(S) tab 或未知模板时报错。
- **[finalize 注销]**: finalizeTabs 早于 detach 清理该会话的全部 main-inject
  content scripts 并清掉 session 状态，不残留。
- **[prelude]**: `session_inject(script="visibility")` helper（RPC
  injectMainScript）+ `_help` 列表 + docstring；单测覆盖。
- **[单测]**: 新增 `main-inject.test.mjs` 4 用例（注册参数/幂等与导航重定
  向/错误路径/finalize 注销）。踩坑：Node 新版 `assert.deepEqual` 并入 strict
  语义，跨 vm realm 的数组原型不同导致误判不等——断言改元素级比较。
- **[打包/安全]**: 打包清单加入新文件；SECURITY.md 新增 MAIN-world 注入边界
  （会话作用域、finalize 回收、模板为扩展自有、调用者信任模型）。
- 全套通过：扩展 7 个测试文件、prelude 33 例、`go test ./...`。

### 🧠 Design Intent (Why)

数据面方案（主动 GraphQL / 被动 Network 捕获）是主线；本次是按用户要求把
"页面脚本前注入"的**平台原生路线**搭出来存为备份：chrome.debugger 路线已被
marker 实验证伪（导航即失活），content script 是扩展一等公民、导航天然存活。
MVP 用静态模板（动态源码受异步通道限制且核心补丁就是这些模板）。生命周期
严格会话级（注册→finalize 回收），安全边界写入 SECURITY 文档。

### 📁 Files Modified

- `apps/chrome-extension/session-inject-main.js`（新增）
- `apps/chrome-extension/background.js`
- `apps/chrome-extension/main-inject.test.mjs`（新增）
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `scripts/package-chrome-extension.sh`
- `docs/SECURITY.md`
- `docs/exec-plans/active/2026-08-26-session-inject-main-world.md`（新增）
- `docs/histories/2026-08/20260827-0123-session-inject-main-world.md`（本文件）

### ⏭ Next

真机验证（需用户同意 reload 扩展后）：open_tab x.com → `session_inject("visibility")`
→ 检查 `window.__obuMainInject` 与 DOM 装载行为；验证通过后可评估合入主线策略。

## [2026-08-27 02:15] 真机验证完成（reload 扩展后）

用户同意 reload 扩展。验证事实与修正：

- 扩展 unpacked 目录 = 主仓库 `apps/chrome-extension`；验证时将分支两份文件
  （background.js、session-inject-main.js）同步过去；`chrome.runtime.reload()`
  （临时 popup 钩子）触发 SW 重读磁盘后成功加载新代码；验证完主仓库还原 HEAD。
- 修正一个真实 API 差异：`chrome.scripting` 的 `runAt` 枚举是小写 `document_start`
  （CDP Page 域才是 documentStart 风格），已修 background.js + 单测断言。
- 验证结果：`__obuMainInject` 标记存在（注入确实在页面脚本前、MAIN world 执行）；
  最小化窗口下 `visibilityState` 读作 visible 且 `hasFocus:false`（覆盖生效、不抢
  焦点）；虚拟列表结构被 IO 模拟唤醒（页高 797→9669）但帖子数仍 4——x.com feed
  数据依赖 app boot 后 GraphQL，该 profile boot 被 Cloudflare 卡死（旧根因），
  与注入无关。结论：MAIN-world 注入路线成立，作为备份能力可用；数据侧仍走数据面
  方案。验证过程全真机，无遗留（测试窗口/调试 tab 已清、popup.js 已还原）。