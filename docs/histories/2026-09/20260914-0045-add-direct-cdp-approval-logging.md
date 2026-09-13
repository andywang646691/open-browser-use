## [2026-09-14 00:45] | Task: Add Direct CDP approval diagnostics

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `gpt-5.6-luna`
- **Runtime**: `darwin-arm64`

### 📥 User Query

> 在每次弹出 Direct CDP 授权窗口时记录日志和触发原因。

### 🛠 Changes Overview

- 新增本地 JSONL diagnostics log，默认写入 macOS 的 `~/Library/Logs/OpenBrowserUse/native-host.log`，权限为 0600；支持 `OBU_LOG_FILE` 覆盖路径。
- 记录 native host 启动、退出、监听、Direct CDP 初始化、profile/DevToolsActivePort 失败、Browser Root WebSocket 连接与断开重连。
- 记录每次可能触发授权的请求，包括 method、CDP mode、session/turn、deadline 和触发原因。
- 记录 macOS approval helper 的启动、授权 sheet 点击、banner helper 和失败信息，并传递 parent PID/reason。
- Python 脚本路径同样传递授权触发原因。

### 🧠 Design Intent (Why)

原生 host 的 stderr 会被 Chrome Native Messaging 丢弃到 `/dev/null`，无法事后判断授权窗口是由新 host、CDP 重连还是具体请求触发。新增轻量 best-effort 本地日志，不影响自动化主链路，同时保留足够信息进行跨进程关联。

### 📁 Files Modified

- `internal/diaglog/diaglog.go`
- `internal/host/relay.go`
- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/script.go`
- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/mac_approval.go`
- `cmd/open-browser-use/mac_approval_darwin.go`
- `cmd/open-browser-use/mac_approval_other.go`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/SECURITY.md`
