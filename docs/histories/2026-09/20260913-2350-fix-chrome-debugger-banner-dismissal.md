## [2026-09-13 23:50] | Task: Fix Chrome debugger banner dismissal

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `gpt-5.6-luna`
- **Runtime**: `darwin-arm64`

### 📥 User Query

> 已知外部 AppleScript 可以关闭 Chrome debugger banner，请修复仓库中的实现。

### 🛠 Changes Overview

**Scope:** macOS CLI helper

- 将 banner dismissal 从原先的 JXA 实现切换为嵌入式 AppleScript。
- 复用可正常工作的 Accessibility tree 遍历和 `AXPress` 关闭逻辑。
- 保留设置/Disable 控件排除，避免误点击“在设置中关闭”。
- 不再强制要求 banner 是 `AXApplicationAlertDialog`，兼容 Chrome 不同版本的 Accessibility 暴露形式。
- 同步更新架构、可靠性和安全文档。

### 🧠 Design Intent (Why)

外部验证成功的脚本使用 AppleScript 的完整 Accessibility tree 遍历，而原 JXA 实现要求特定 dialog subrole，导致实际 banner 未被发现。使用相同的 AppleScript 机制，同时维持窄化的关闭按钮匹配和安全保护。

### 📁 Files Modified

- `cmd/open-browser-use/mac_approval_darwin.go`
- `cmd/open-browser-use/mac_dismiss_banner.applescript`
- `cmd/open-browser-use/mac_dismiss_banner.js`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/SECURITY.md`
