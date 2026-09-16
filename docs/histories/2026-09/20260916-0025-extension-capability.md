## [2026-09-16 00:25] | Task: 对齐 Chrome DevTools MCP Extension 能力

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `OpenAI Codex`
- **Runtime**: `pi coding agent`

### 📥 User Query

> 让项目加上同等 Extension 能力

### 🛠 Changes Overview

**Scope:** `internal/cdp`、`internal/host`、`cmd/open-browser-use`、Python/JS/Go SDK、skill 与仓库文档。

**Key Actions:**

- **[Extension CDP domain]**: 新增 `Extensions.getExtensions`、`Extensions.loadUnpacked`、`Extensions.triggerAction`、`Extensions.uninstall` 的 Direct CDP 封装，并以现有 browser-root 连接复用实现 reload；trigger 会通过 Target discovery 和 flat session 精确解析 synthetic tab target，兼容 Chrome 不在初始列表中暴露 tab target 的行为。
- **[OBU RPC and CLI]**: 新增 `listExtensions`、`installExtension`、`reloadExtension`、`triggerExtensionAction`、`uninstallExtension` RPC 和五个 CLI 子命令；trigger 支持显式 tab 或当前 session 唯一 active tab，并通过扩展精确解析 target id。
- **[SDK and script surface]**: 为 Python prelude、JS SDK、Go SDK 和保留 MCP server 增加对应 API；安装路径要求绝对 unpacked extension 目录。
- **[Tests and docs]**: 增加 Direct CDP extension lifecycle、脚本、JS/Python wrapper 测试，更新架构、可靠性、安全、skill、SDK README 和发布记录。

### 🧠 Design Intent (Why)

Chrome DevTools MCP 的 Extension 工具依赖 browser-root `Extensions.*` 域，不能通过页面的 `chrome.debugger` 路径等价实现。OBU 保留自己的真实用户 Chrome、profile 精确定位和 ownership 约束，因此采用 Direct CDP 实现生命周期操作，并禁止按 URL/title 猜测 trigger target。当前能力只覆盖 Chrome CDP 支持的 unpacked extension，不恢复已下线的 MCP 产品入口。

补充验证表明，当前 Chrome 153 在不显式传入 `--enable-unsafe-extension-debugging` 时也能通过远程调试端口执行 `Extensions.getExtensions`、`Extensions.loadUnpacked` 及完整的 action smoke；因此该 flag 不是 OBU 当前实现的硬性要求，而是部分 Chrome/启动模式的兼容选项。

### 📁 Files Modified

- `internal/cdp/extensions.go`
- `internal/cdp/extensions_test.go`
- `internal/cdp/manual_extension_smoke_test.go`
- `internal/cdp/manager.go`
- `internal/host/relay.go`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/mac_approval.go`
- `cmd/open-browser-use/mcp.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `apps/chrome-extension/extension-capability.test.mjs`
- `packages/open-browser-use-js/src/index.ts`
- `packages/open-browser-use-js/test/frame.test.mjs`
- `packages/open-browser-use-python/open_browser_use/client.py`
- `packages/open-browser-use-python/test_client.py`
- `packages/open-browser-use-go/client.go`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/SECURITY.md`
- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/tabs.md`
- `packages/open-browser-use-{js,python,go}/README.md`
- `docs/releases/feature-release-notes.md`
- `docs/exec-plans/completed/2026-09-16-extension-capability.md`
