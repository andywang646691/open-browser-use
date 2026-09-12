## [2026-09-12 01:30] | Task: Implement Direct CDP with Extension Fallback

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `gemini-3.8-flash`
- **Runtime**: `darwin-arm64`

### 📥 User Query

> 1) Add Direct CDP while keeping the external API shape unchanged.
> 2) DO NOT delete the existing extension-mediated CDP path; keep it as a selectable backup/fallback.
> macOS-first personal-browser remote debugging + launcher-side `mac-approve` + safe banner dismissal, following the community-proven patient-handshake pattern.
> Intercept CDP-related JSON-RPC inside native host/Go layer; connect to browser root WebSocket via DevToolsActivePort; Target.attachToTarget(flatten=true); exact tabId->targetId mapping from extension via narrow RPC without heuristics.
> OBU_CDP_MODE=auto|direct|extension (default auto). Preserve deadlines/timeouts, late-response quarantine, ownership semantics, disconnect cleanup, lazy attach per owned tab. Audit clipboard and file chooser helpers.

### 🛠 Changes Overview

**Scope:** `apps/chrome-extension`, `cmd/open-browser-use`, `internal/cdp`, `internal/host`, `docs`, `skills`

**Key Actions:**

- **[Extension Narrow RPC]**: 在 `apps/chrome-extension/background.js` 中新增窄作用域 `getTargetMapping` RPC，严格校验 `session_id`、`turn_id` 与所有权，重载 discarded 标签页并读取 `chrome.debugger.getTargets()` 返回精确 `{ tabId, targetId }`，绝不跨 profile 模糊匹配，且不触发 `chrome.debugger.attach`。
- **[Direct CDP Engine]**: 新增 `internal/cdp` 包，包含 `PortInfo` 解析与严格 loopback 安全校验、基于 `gorilla/websocket` 的客户端（带超时-响应竞争防护与请求级迟到 attach 自动回收）、`Manager` 会话管理、多路复用、超时隔离、主 frame `Page.frameNavigated` 异步唤醒重放。
- **[Native Host 模式拦截与路由]**: 在 `internal/host/relay.go` 中拦截 `attach`, `executeCdp`, `detach`, `releaseTabs`, `closeUserTab`, `finalizeTabs`, `turnEnded`, `readClipboardText`, `writeClipboardText`, `readClipboard`, `writeClipboard`, `waitForFileChooser`, `setFileChooserFiles`。在 `auto` 模式下为每个 `(sessionID, tabID)` 选定路由并持久化保持 sticky；支持显式 `direct` 和 `extension` 模式；Direct Browser Root 初始化采用 daemon-wide singleflight/patient handshake，多个 caller 共享一条 pending WebSocket，清理方法绝不误触 direct 初始化。
- **[Exact Profile 定位]**: 在初始化 Direct CDP 时向扩展实例发送内部 `getInfo`，获取 `extensionId` + `extensionInstanceId` 反查 LevelDB 获取精确 profile 根目录与 DevToolsActivePort，严禁默认猜测。
- **[文件选择器与剪贴板接管]**: Direct 模式完整实现文件选择器（保留 `backendNodeId`、`mode` 校验、session 隔离、defer 关闭拦截）与完整剪贴板方法，审计 JS `exceptionDetails` 异常，避免重入扩展端 debugger。
- **[Caller Mode 隐式传递 & 时限透传]**: 在 Go CLI `applySessionDefaults` 和 Python prelude `script_helpers.py` 中自动将 `OBU_CDP_MODE` 注入为隐藏请求参数 `_cdpMode`，并将调用方 `_deadlineUnixMs` 透传至 Direct Context 与内部 `getTargetMapping`。
- **[macOS Client Approval]**: 参考 `browser-harness` 的成熟 personal-Chrome 模式，将授权从 native host 移到 CLI/Python launcher。Direct-capable 请求写入 socket 后启动隐藏 `__mac-approve`，使用 `System Events -> Chrome windows -> sheets` 精确匹配中英文授权框，只点击 `Allow` / `允许`。真机已验证首次授权可全自动完成；automation banner 的关闭保持独立、best-effort，失败不会影响 Direct CDP 握手或任务结果，并严格排除“在设置中关闭”/disable controls。
- **[Patient Browser Handshake]**: Direct client 的 local Browser WebSocket 不再设置 transport-local handshake timeout；daemon 仅保留一个 pending 初始化，caller deadline 只停止 caller 等待而不取消共享握手。批准后长期复用；Browser WebSocket 断开后 stale Manager 被丢弃并懒重建。
- **[测试与文档]**: 补全扩展端测试、CDP 端口/安全性测试、Client 竞争与晚到回收测试、Manager 核心生命周期与文件选择器测试、Relay 单一 pending handshake/模式/生命周期测试、CLI/Python client-approval 参数传递测试；同步更新 ARCHITECTURE, RELIABILITY, SECURITY 与 tabs reference，并完成 Direct/Extension 双路真实 Chrome E2E。

### 🧠 Design Intent (Why)

- Direct CDP 将页面自动化流量从 `chrome.debugger` transport 中移出，减少 MV3/service-worker 与不可取消 extension Promise 对 CDP 主链路的影响；personal Chrome 自身仍会显示远程调试授权/banner，因此由 launcher-side helper 负责显式批准，banner 仅做不影响主链路的 best-effort 收起。扩展端现有 CDP 完整保留作为稳定回退，实现零外部破坏性变动。
- 多 profile Chrome 运行时，Root DevTools WebSocket 能够观测到全部 target，因此必须由对应 profile 的扩展实例提供 exact `tabId -> targetId` 映射，杜绝越界访问。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/target-mapping.test.mjs`
- `internal/cdp/port.go`
- `internal/cdp/port_test.go`
- `internal/cdp/client.go`
- `internal/cdp/discovery.go`
- `internal/cdp/manager.go`
- `internal/cdp/manager_test.go`
- `internal/host/relay.go`
- `internal/host/relay_cdp_test.go`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/main_test.go`
- `cmd/open-browser-use/script.go`
- `cmd/open-browser-use/mac_approval.go`
- `cmd/open-browser-use/mac_approval_darwin.go`
- `cmd/open-browser-use/mac_approval_other.go`
- `cmd/open-browser-use/mac_approve.applescript`
- `cmd/open-browser-use/mac_dismiss_banner.js`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `go.mod`
- `go.sum`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/SECURITY.md`
- `skills/open-browser-use/references/tabs.md`
- `docs/exec-plans/completed/2026-09-12-direct-cdp-architecture.md`
