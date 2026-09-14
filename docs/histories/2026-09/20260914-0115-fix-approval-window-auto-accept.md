## [2026-09-14 01:15] | Task: 修复 OBU 授权窗口自动同意

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `gpt-5.6-luna`
- **Runtime**: `darwin-amd64`

### 📥 User Query

> 在执行 OBU 任务时，授权窗口打开，无法自动同意。

### 🛠 Changes Overview

**Scope:** macOS stable approval helper

- 新增独立的 `open-browser-use-mac-approval-helper`，只负责 Chrome 授权 sheet 和 automation banner 的 Accessibility 操作。
- helper 以稳定的 macOS app bundle 安装到 `~/Library/Application Support/OpenBrowserUse/approval-helper/`，保持稳定路径和 TCC 身份；CLI/Python/native host 不再执行 `osascript`。
- helper 首次运行调用 `AXIsProcessTrustedWithOptions` 请求 macOS 显示系统辅助功能授权提示；CLI 与 Python task 改为调用同一个 helper，用户只需给该 helper 开启辅助功能权限。
- helper 以 macOS app bundle 形式构建，并使用 `codesign` 签名；若本机没有 Apple signing identity，则使用 ad-hoc 签名，配置 `OBU_MAC_CODESIGN_IDENTITY` 后可改用 Developer ID / Apple Development 身份。
- 增加 helper 编译回归测试。

### 🧠 Design Intent (Why)

Accessibility 权限应绑定到一个稳定、职责单一的 helper，而不是 `/usr/bin/osascript`、终端、IDE 或频繁重建的 native host。这样 macOS 设置中只需授权一个 OBU helper，同时保留精确的 `Allow` / `允许` 和关闭 banner 行为。

### 📁 Files Modified

- `scripts/macos/open-browser-use-mac-approval-helper.m`
- `scripts/build-mac-approval-helper.sh`
- `scripts/install-local.sh`
- `cmd/open-browser-use/mac_approval_darwin.go`
- `cmd/open-browser-use/script.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/mac_approval_test.go`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/SECURITY.md`
