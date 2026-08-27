# 架构总览

这个仓库当前目标是实现 Open Browser Use：一套开源 Browser Use 风格的
Chrome 浏览器自动化基础设施。

当前 `main` 分支只保留 Chrome route：MV3 Chrome extension + Go native
messaging host + CLI。JS/Python/Go SDK 与 MCP server 的源码仍保留在仓库内
并被测试覆盖，但不再对外暴露：不发布到 npm/PyPI，`obu mcp` 已从 CLI 下线，
README 与 skill 不再宣传这两种模式。

已完成的 Chrome route 执行计划：

- `docs/exec-plans/completed/2026-05-08-open-browser-use-chrome-route.md`

已有 Browser Use / Codex extension 逆向知识沉淀在：

- `docs/wiki/browser-client/`
- `docs/references/`

## 预期的仓库结构

- `apps/chrome-extension/`：Open Browser Use MV3 Chrome extension，包含
  readable service worker、cursor content script 和 popup。
- `cmd/open-browser-use/`：Go native messaging host 和 CLI 主入口。二进制
  正式名是 `open-browser-use`，可通过同路径 symlink 或 shell alias 暴露为
  `obu`。
- `internal/host/`：Go native messaging stdio 到本地 Unix socket 的中继。
- `internal/wire/`：Open Browser Use native frame codec。
- `packages/browser-client-rewrite/`：当前 Browser Use client 的可读重写
  和协议行为参考。
- `packages/browser-use-protocol/`：Browser Use JSON-RPC native pipe frame、
  请求/响应类型和测试工具。
- `packages/open-browser-use-cli/`：npm 上发布的 `open-browser-use` 二进制
  CLI 包，暴露 `open-browser-use` 和 `obu` 两个命令，发布时只打包 Go CLI
  预构建二进制，不打包 Chrome extension。
- `packages/open-browser-use-js/`：JavaScript/TypeScript SDK 源码（内部保留，
  不再发布到 npm）。
- `packages/open-browser-use-python/`：Python SDK 源码（内部保留，不再发布
  到 PyPI），import 模块名为 `open_browser_use`。
- `packages/open-browser-use-go/`：Go SDK 源码（内部保留，不再作为对外
  import path 宣传），package name 为 `obu`。
- `skills/open-browser-use/`：面向 AI Agent 的 Open Browser Use skill，说明
  如何安装、验证和使用浏览器插件、CLI、SDK 与 Browser Use 风格协议。
- `packages/`：其他跨应用复用的库、契约和共享能力。
- `infra/`：部署、基础设施和环境定义。
- `scripts/`：仓库级自动化脚本，供人和 Agent 直接调用。
- `docs/`：仓库知识库，也是本地规则和上下文的正式来源。
- `docs/wiki/`：长期演进的主题知识库，用于沉淀可持续追加的调研、
  逆向和架构理解。

## 边界建议

- 业务逻辑优先沉淀到可复用包里，不要一开始就散落在各个 app 中。
- 基础设施和运行编排要显式版本化，不要藏在手工操作里。
- 避免隐式跨包耦合；一旦仓库成形，就把允许的依赖方向写清楚。
- 只要架构有变化，就同步更新这份文档。

## Open Browser Use Chrome Route

Chrome route 的目标拓扑：

```text
upper-layer runtime / CLI
  -> /tmp/open-browser-use/<uuid>.sock
  -> open-browser-use Go native host
  -> Chrome Native Messaging stdio
  -> Open Browser Use MV3 service worker
  -> chrome.tabs / chrome.debugger / chrome.history / chrome.tabGroups
  -> user's real Chrome profile
```

Chrome native messaging host name 固定为：

```text
com.ifuryst.open_browser_use.extension
```

Chrome native messaging host name 只能使用小写字母、数字、underscore 和
dot；hyphen 版本 `com.ifuryst.open-computer-use.extension` 会被
`chrome.runtime.connectNative` 直接拒绝。

当前 M1 骨架已经提供：

- native-endian 4-byte length-prefixed JSON frames。
- `open-browser-use host`：启动 Chrome native messaging stdio 到 SDK Unix
  socket 的中继；默认 socket 路径是
  `/tmp/open-browser-use/<uuid>.sock`，并写入
  `/tmp/open-browser-use/active.json` 供 CLI 自动发现当前活跃 socket。
  Chrome native messaging 以 `chrome-extension://...` origin 参数启动二进制时，
  CLI 会自动进入 host mode。
- 无参数运行 `open-browser-use` 或 `obu` 输出 CLI 版本、浏览器插件检测状态、
  插件版本和下一步安装/升级命令，不再启动 native host；手工启动 host 使用
  `open-browser-use host`。
- `open-browser-use manifest`：输出 Chrome native messaging host manifest。
- `open-browser-use install-manifest`：把 native messaging host manifest
  写入 Chrome 默认位置，或通过 `--output` 写到指定路径。默认 extension id
  是 Chrome Web Store 版 `bgjoihaepiejlfjinojjfgokghnodnhd`。
  Windows 默认写入
  `%LOCALAPPDATA%\OpenBrowserUse\NativeMessagingHosts\com.ifuryst.open_browser_use.extension.json`，
  并注册
  `HKCU\Software\Google\Chrome\NativeMessagingHosts\com.ifuryst.open_browser_use.extension`
  的默认值指向该 manifest。
- `open-browser-use setup`：显式安装流程，先调用 native host manifest
  注册，再写入 Chrome External Extensions JSON。只有检测到浏览器插件未安装或
  版本低于当前 CLI 期望版本时，CLI 才打开 Chrome Web Store 正式扩展页，引导
  用户手动安装或启用扩展。macOS/Windows 仍可能需要用户在 Chrome 中确认启用
  扩展并重启；Linux 的 External Extensions 写入默认使用 Chrome 官方系统路径，
  可能需要更高权限。
- `open-browser-use setup beta`：Chrome Web Store 临时不可用时的备用路径，
  注册 native host 后从 GitHub Releases 下载最新
  `open-browser-use-chrome-extension-*.zip`。CLI 会在本地 unpacked 目录和待拖入
  Chrome 的 ZIP 中写入稳定 beta public key，用该 id 注册 native host allowed
  origin；只有检测到浏览器插件未安装或版本低于当前 CLI 期望版本时，才打开
  `chrome://extensions/` 和 Finder/文件管理器，引导用户把这个 ZIP 拖到扩展页面
  手动安装；GitHub Release 中的正式 zip 本身保持为 Chrome Web Store 上传包，
  不再预写 beta key。setup 过程中如果本机已有可用的 `npx skills`，会
  best-effort 执行 `npx skills update open-browser-use -g -y` 更新已有 agent
  skill；未检测到 skills 时只展示 Codex 和 Claude Code 的安装命令。
- manifest 的 `path` 默认统一写入稳定 native host link：
  macOS 为
  `~/Library/Application Support/OpenBrowserUse/native-host/open-browser-use`，
  Linux 为 `~/.local/share/open-browser-use/native-host/open-browser-use`。
  Windows 为
  `%LOCALAPPDATA%\OpenBrowserUse\native-host\open-browser-use.exe`。macOS/Linux 上
  `install-manifest --path` 表示这个稳定 link 指向的真实二进制 target；Windows
  上会把 target 复制到稳定 exe 路径，避免依赖开发者模式或管理员权限创建 symlink。
- npm 包 `open-browser-use` 是 CLI 二进制分发入口，安装后提供
  `open-browser-use` 和 `obu`；`postinstall` 只提示用户运行显式
  `open-browser-use setup`。
- SDK 发布包在 npm 和 PyPI 上统一命名为 `open-browser-use-sdk`，避免和 npm
  CLI 包 `open-browser-use` 混淆。JavaScript 使用
  `import { ... } from "open-browser-use-sdk"`；Python distribution 安装名是
  `open-browser-use-sdk`，代码 import 模块名仍是 `open_browser_use`。
- Homebrew formula 从 GitHub Release 下载平台匹配的预编译 CLI tarball，
  安装后提供 `open-browser-use` 和 `obu`，并在 caveats 中提示用户运行显式
  `open-browser-use setup`；安装阶段不依赖 Go，也不在用户机器上编译。
- CLI 命令层使用 Cobra 实现；Chrome native messaging
  `chrome-extension://...` origin 参数启动时会绕过 Cobra，直接进入 host mode。
  这依赖 Chrome Native Messaging 的标准启动形状：MV3 service worker 调用
  `chrome.runtime.connectNative(...)` 后，Chrome 以 host manifest 中的可执行文件
  路径启动进程，并把调用方 extension origin 作为第一个 argv 传入。manifest
  自身不承载额外自定义 argv，因此 Chrome extension 专用启动信号使用这个
  Chrome 提供的 origin argv。Windows Chrome 还可能只暴露 `--parent-window=<handle>`
  形状，因此 CLI 也把该参数识别为 native host mode。
- `open-browser-use call`：unrestricted JSON-RPC 入口，允许上层应用发送
  任意 method/params；未显式传入 `--socket` 时会优先读取 active socket
  registry。registry 缺失时，CLI 会按修改时间扫描 `--socket-dir` 下的
  `*.sock` 并连接最新可用 socket，连接成功后修复 active registry；如果
  registry 指向不可连接的旧 socket，CLI 会移除 stale registry entry 和对应
  stale socket file，再尝试目录扫描，避免后续命令持续命中同一个失效 socket。
- `open-browser-use run`：line-oriented action plan 入口，支持一次执行多条
  CLI action，保留同一个 session/turn，并让 `open-tab`/`claim-tab` 设置默认
  tab，供后续 `wait-load`、`page-info`、`navigate`、`cdp` 等 tab-scoped action
  复用；默认和普通 CLI 子命令使用同一个 `obu-cli` browser session，因此
  一次 `finalize-tabs` 可以清理两种调用方式打开的 tabs；需要隔离 tab group
  和清理范围时可以显式传 `--session-id`。它不是通用编程语言，适合 shell、
  CI 和轻量 agent 编排。
- 无参数运行 `obu` 且 stdin 为管道输入（如 `obu <<'PY' ... PY` heredoc）时
  进入真 Python 脚本模式：Go CLI spawn 系统 `python3`，执行 go:embed 的
  prelude（`cmd/open-browser-use/script_helpers.py`）与用户脚本拼接后的
  源码；helper 预导入（`user_tabs()`、`tabs()`、`claim_tab(id)`、
  `open_tab(url)`、`navigate(tab_id, url)`、`history(query)`、
  `name_session(name)`、`cdp("Domain.method", **kwargs)`、`evaluate(js)`、
  `finalize_tabs(keep=[...])`），完整 Python 语言可用（if/for、列表推导、
  f-string）；API 契约写在 helper docstring 中，脚本内
  `help(name)`/`_help()` 可随时自省签名与返回结构；CDP 参数以结构化
  kwargs 传入，由 Python runtime 编码，模型不写 JSON/shell 转义；
  stdout/stderr 原样透传、python 退出码透传到 CLI。socket 由 Go 侧按
  `--socket`/registry/profile 路由解析后经 `OBU_SCRIPT_SOCKET` 传给
  prelude，prelude 直接用标准库走 Browser Use JSON-RPC frame 协议。
  `open_tab`/`claim_tab` 设置当前 tab，供后续 `cdp`/`evaluate`
  复用；`finalize_tabs()` 缺省 keep 为空列表。
- 会话、browser、profile 等状态提供环境变量默认值：`OBU_SESSION_ID`、
  `OBU_BROWSER`、`OBU_PROFILE`、`OBU_WINDOW_MODE` 在未显式传对应 flag
  时作为默认值兜底，flag 仍然优先。
- MCP 模式已下线：`cmd/open-browser-use/mcp.go` 的 stdio MCP server 源码
  仍保留在仓库内且被测试覆盖，但 `newMCPCommand` 不再注册到 CLI，
  `obu mcp` / `open-browser-use mcp` 不可用，README 与 skill 也不再有
  MCP 指引。
- CLI 便捷子命令覆盖当前核心能力：`ping`、`info`、`tabs`、
  `user-tabs`、`history`、`open-tab`、`navigate`、`claim-tab`、
  `finalize-tabs`、`name-session`、`cdp`、`move-mouse`、
  `wait-file-chooser`、`set-file-chooser-files`、`turn-ended`、`profiles`。
- Multi-browser / multi-profile 支持：`open-browser-use profiles` 扫描 Chrome
  Stable、Chrome Beta 和 BitBrowser user-data roots，在每个 profile 目录中
  通过 CRX 与 unpacked 两条路径找到已经装好插件的 profile，附带 browser id、
  browser display name、profile directory 和展示名（来自
  `Local State.profile.info_cache`）。所有面向用户的命令支持
  `--browser <chrome|chrome-beta|bitbrowser|displayName|instance>` 和
  `--profile <directory|displayName>`：CLI 枚举 `socket-dir` 下的 `.sock`
  文件，对每个连通的 socket 调用 `getInfo`，把
  `metadata.extensionInstanceId` 反查到 browser/profile（grep 每个支持 browser
  root 的 `Local Extension Settings/<extension-id>/*.{log,ldb}`），按 selector
  选出匹配 socket。selector 不匹配时 CLI 列出当前已连通
  target 列表，提示用户打开对应 browser/profile。扩展检测的候选 id 包含商店版、
beta 版以及本 fork 的 `forkExtensionID`（派生自仓库 manifest 的 `key`），并支持
`OBU_EXTENSION_IDS` 环境变量注入额外候选（fork 换 key 后无需改代码）；Load
unpacked 安装通过各 profile 的 `Secure Preferences` 里 `extensions.settings[id].path`
发现——此前候选列表只含上游 id，`profiles` 对 fork 的 unpacked 安装完全不可见，
曾导致 profile 探测结果误判。Windows 当前覆盖 Chrome
  Stable 和 Chrome Beta 的 `%LOCALAPPDATA%\Google\...\User Data` roots；BitBrowser
  root 仍只在 macOS 路径中自动发现。未显式选择时仍保留
  `active.json` 快速路径；`active.json` 缺失或失效时，CLI 会扫描 socket 目录并
  修复 registry，因此不需要重装扩展。
- MV3 extension core handlers：`getInfo`、`createTab`、`getTabs`、
  `getUserTabs`、`getUserHistory`、`claimUserTab`、`finalizeTabs`、
  `nameSession`、`attach`、`detach`、`executeCdp`、`moveMouse`、
  `waitForFileChooser`、`setFileChooserFiles`、`waitForDownload`、
  `downloadPath`、`readClipboardText`、`writeClipboardText`、
  `readClipboard`、`writeClipboard`、`turnEnded`。
- Session state persists the Chrome tab group id, tab origins, group title,
  deliverable group id, and logical active tab id in `chrome.storage.local` so
  MV3 service worker restarts can recover session tab listing semantics.
  新 session 的默认任务组名是 `Task - OBU`，上层 runtime 应通过
  `nameSession`/`name-session` 按 `<short task> - OBU` 设置更具体的任务名；
  `deliverable` tabs 统一移动到共享 `✅ Open Browser Use` tab group，`handoff`
  tabs 保留在当前任务组。每个 Chrome 窗口至多存在一个 `✅ Open Browser Use`
  分组：`moveToDeliverables` 按 `windowId` 调用 `chrome.tabGroups.query` 找出已有
  同名分组，合并任何重复（如 session restore 带回的旧分组）后再加入新 tab，
  仅在该窗口完全没有同名分组时才新建。
  `turnEnded` and `finalizeTabs` clear active session state when control ends
  so download notifications only fire while a browser session is active.
- 隔离窗口模型：新 session 的标签页默认全部落进一个专属后台 Chrome 窗口
  （`chrome.windows.create`：`focused:false`、tabs `active:false`、非最小化、
  正常尺寸近全屏居中定位（小视口会让 SPA 虚拟列表只装少量条目）），全程不抢焦点、不打扰用户现有窗口；`session.windowId`
  持久化在 session state 中，窗口被用户关闭或 Chrome 重启后，下次创建 tab
  时自动重建。窗口被遮挡/最小化时 tab 报 `document.visibilityState==="hidden"`
  （无 CDP 命令可伪造可见性），且 compositor 会对 occluded 窗口节流、挂起
  懒加载渲染管线——OBU 对隔离窗口 tab 在首次 debugger attach 时自动应用
  CDP 层后台唤醒（`Emulation.setFocusEmulationEnabled(true)` +
  `Page.setWebLifecycleState({state:"active"})`，best-effort、逐命令容错、
  detach 即复位、不抢焦点），并在主 frame `Page.frameNavigated` 时自动重放
  （导航会重置 lifecycle 为 hidden+节流）；读 `visibilityState`/`document.hidden`
  的站点仍需 `session_inject("visibility")`（MAIN-world DOM 注入）配合。
  `window_mode` 参数（`OBU_WINDOW_MODE` 环境变量，`open-tab
  --window-mode` flag）决定是否隔离：缺省/`isolated` 即隔离，`user` 退出
  隔离、沿用旧行为（新标签页进用户聚焦窗口）。一旦 session 建立了隔离
  窗口就永久隔离。**隔离会话的成员关系以“归属窗口”追踪**（`tabs.query({windowId})` +
  `tabOrigins`），同时仍会在隔离窗口内建立 tab group 做可视化组织：
  `chrome.tabs.group` 会把新组钉在*当前*（聚焦）窗口并把 tab 拽
  过去（`createProperties.windowId` 在部分 Chrome 构建上被无视），因此建组用
  “替身 tab + 修复”流程：先让一个临时 about:blank 替身去踩建组路径，组若
  落到错误窗口就用 `tabGroups.move(groupId, {windowId})` 把整组搬回隔离窗
  口，随后删除替身、真实会话 tab 再归组，全程不离开隔离窗口；建组失败则
  静默退化为“无组、窗口式追踪”。后续 tab 因组已锚定在隔离窗口内，
  `tabs.group({groupId, ...})` 同窗口归组天然可靠。  user 模式（旧行为）与交付物分组。`claimUserTab` 把用户 tab
  `chrome.tabs.move` 进隔离窗口再入追踪；`getUserTabs` 排除所有 session
  自有窗口内的 tab，防止 agent 把自己的工作页当用户 tab 再 claim。
  `finalizeTabs` 无保留时关闭该窗口（仅当窗口已空，绝不连带关闭残留的
  用户 tab）；`deliverable` tab 先移到用户可见窗口再进 `✅ Open Browser
  Use` 分组，`handoff` 保留时窗口存活以便下轮继续。
- MV3 extension event forwarding：`chrome.debugger.onEvent` 转发为
  `onCDPEvent`，`chrome.downloads` 创建/变更转发为 `onDownloadChange`，
  cursor content script 会回报 cursor arrival 以支持 `moveMouse`
  等待落点。file chooser 通过 CDP `Page.setInterceptFileChooserDialog` 和
  `Page.fileChooserOpened` 事件截获，再使用 `DOM.setFileInputFiles` 写入
  本地文件路径。download wait/path 和 clipboard helpers 由 extension backend
  暴露给 SDK；agent-facing Browser Use command rewrite 还覆盖 DOM CUA、media
  download、element info/screenshot、locator bulk reads、generic tab export 和
  clipboard commands。
- JS、Python 和 Go SDK（源码保留在 `packages/open-browser-use-js|python|go`）：
  三个 SDK 直接连接 Unix socket 发送 Browser Use JSON-RPC（JS 支持订阅
  notification，Python/Go 在同步 request loop 中分发并跳过 notification，
  Go 可自动读取 active socket registry），在原子 client 之外提供高层
  browser/tab API（`tab.goto`、`tab.waitForLoadState`、
  `tab.playwright.domSnapshot` 等 Browser Use 风格封装）以及
  download/clipboard convenience wrappers 和 unrestricted `request`。
  当前不对外发布：不刊登 npm/PyPI，README 与 skill 不再宣传；需要恢复
  对外能力时重新开启发布流水线即可。

仓库内保留的 SDK/MCP 实现不内置 Codex 风格的站点限制、turn policy 或二次
确认。上层应用可以自由调用；如果将来恢复对外暴露，生产集成需要的安全策略
应在上层 runtime 或 host 前置网关中实现。

## 新项目需要补齐的内容

- 核心产品面和运行拓扑。
- 包分层与依赖边界。
- 数据流与存储模型。
- 可观测性方案和本地开发模式。
