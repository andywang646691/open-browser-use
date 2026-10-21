## [2026-10-21 02:10] | Task: 加固 macOS 远程调试授权助手

### 🤖 Execution Context

- **Agent ID**: `claude-ai-chat`
- **Base Model**: `claude-opus-5-5`
- **Runtime**: `claude.ai + NexusDock (AgentDock macOS node)`

### 📥 User Query

> 审查 OBU 中 mac-approve 部分是否正确，然后修复审查发现的问题。

### 🛠 Changes Overview

**Scope:** macOS approval helper、CLI / Python launcher、架构 / 可靠性 / 安全 / 排障文档。

**Key Actions:**

- **helper 只保留原生 Accessibility**：删除 AppleScript 路径与死脚本；授权框只在 window sheet / dialog 中查找并跳过网页内容；infobar 精确定位到 automation 那一条。
- **TCC 身份**：helper 以 responsibility disclaim 重新 exec 自身，权限只认 helper bundle。
- **扫描 PWA 应用壳**：Chrome 会把授权框挂在 `com.google.Chrome.app.*` 承载的 PWA 窗口上。
- **合并子命令**：`approve-then-dismiss-wait` 以独立 session 运行，一次性 CLI 退出后仍能收起 banner；Python 路径同样收起 banner。
- **只在需要时启动**：请求 400ms 未响应才启动 helper；单实例锁；调用方退出且无可见授权框时停止。
- **不再阻塞**：后台模式无阻塞弹窗，未授权立即退出码 3；交互引导移到 `setup`；新增 `status` / `probe` / `dump` / `self-test`。
- **可观测性**：helper 自己写 JSONL 诊断；CLI 记录 helper 退出状态。
- **Python 一致性**：按请求实际 `_cdpMode` 判断，与 Go 规则共用同一组方法。

### 🧠 Design Intent (Why)

审查发现原实现在一次性 CLI、Python 路径、无权限、已批准后空转、网页内容误点、infobar 误关等场景下不可靠。真机验证进一步暴露两个根因：helper 被终端 / agent 直接 exec 时 TCC 判定的是父进程权限；授权框常出现在 PWA 窗口，而 PWA 窗口属于独立应用壳进程。修复后批准与收起由 helper 独立完成，并且只在请求确实停在授权框上时才启动。

### 📁 Files Modified

- `scripts/macos/open-browser-use-mac-approval-helper.m`
- `scripts/build-mac-approval-helper.sh`
- `scripts/macos/open-browser-use-approve.applescript`（删除）
- `scripts/macos/open-browser-use-dismiss-banner.applescript`（删除）
- `scripts/macos/open-browser-use-mac-approval-helper.sh`（删除）
- `cmd/open-browser-use/mac_approval.go`
- `cmd/open-browser-use/mac_approval_darwin.go`
- `cmd/open-browser-use/mac_approval_other.go`
- `cmd/open-browser-use/mac_approval_test.go`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/SECURITY.md`
- `skills/open-browser-use/references/troubleshooting.md`
- `docs/exec-plans/completed/2026-10-21-mac-approval-helper-hardening.md`
