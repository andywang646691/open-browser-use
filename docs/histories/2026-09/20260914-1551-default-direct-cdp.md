## [2026-09-14 15:51] | Task: 将默认 CDP 模式改为 Direct

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `gpt-5.6-luna`
- **Runtime**: `darwin-amd64`

### 📥 User Query

> 将 auto 模式改为明确 direct cdp，然后 commit

### 🛠 Changes Overview

**Scope:** Go native host、CLI/Python caller、运行文档

**Key Actions:**

- 将未显式配置 CDP 模式时的默认值从 `auto` 改为 `direct`。
- 保留 `auto` 作为显式 opt-in 的 Direct 优先、扩展 fallback 模式。
- 更新 CLI/Python 请求默认注入、帮助文本、skill/reference 和安全/架构说明。
- 增加默认 Direct 模式的 Go/Python 回归测试。

### 🧠 Design Intent (Why)

默认路径必须明确使用 Direct CDP，避免不可见地退回扩展 `chrome.debugger` 路线并产生 Chrome debugger banner。需要兼容旧环境时，可显式设置 `OBU_CDP_MODE=auto`；需要扩展路线时使用 `OBU_CDP_MODE=extension`。

### 📁 Files Modified

- `internal/host/relay.go`
- `internal/host/relay_test.go`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/main_test.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `docs/ARCHITECTURE.md`
- `docs/SECURITY.md`
- `skills/open-browser-use/references/tabs.md`
