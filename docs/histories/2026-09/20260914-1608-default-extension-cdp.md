## [2026-09-14 16:08] | Task: 默认切换为 Extension CDP

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `gpt-5.6-luna`
- **Runtime**: `darwin-amd64`

### 📥 User Query

> 默认切换为 extension cdp

### 🛠 Changes Overview

**Scope:** Go native host、CLI/Python caller、运行文档

**Key Actions:**

- 将未显式配置 CDP 模式时的默认值从 `direct` 切换为 `extension`。
- 保留 `direct` 作为显式强制模式，保留 `auto` 作为显式 Direct 优先并回退到扩展端的模式。
- 更新 Go/Python 默认请求注入、帮助文本、测试和使用文档。

### 🧠 Design Intent (Why)

默认使用扩展端 CDP，避免用户未主动选择 Direct CDP 时触发 Browser Root 调试授权流程；兼容需要 Direct CDP 的场景可显式设置 `OBU_CDP_MODE=direct`，需要自动降级可使用 `OBU_CDP_MODE=auto`。

### 📁 Files Modified

- `internal/host/relay.go`
- `internal/host/relay_test.go`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/main_test.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `cmd/open-browser-use/mac_approval.go`
- `docs/ARCHITECTURE.md`
- `docs/SECURITY.md`
- `docs/exec-plans/completed/2026-09-12-direct-cdp-architecture.md`
- `skills/open-browser-use/references/tabs.md`
