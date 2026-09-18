## [2026-09-18 11:15] | Task: 修复 Native Host 间歇性失联后的 socket 发现

### 🤖 Execution Context

- **Agent ID**: `01a0b27a-a60a-7c64-b23b-18411248bedc`
- **Base Model**: `gpt-5.6-luna`
- **Runtime**: `relay-cpa`

### 📥 User Query

> Chrome 扩展显示 native host connected，OBU 曾成功操作浏览器，但下一次请求却提示没有连接到 Chrome。

### 🛠 Changes Overview

**Scope:** CLI socket discovery、连接故障恢复、排障文档。

**Key Actions:**

- **保留 Unix socket 路径**：CLI 探测失败时不再删除 `*.sock`。一次短暂连接失败不能证明 native host 已退出；unlink 仍存活 host 的监听路径会让它永久无法被重新发现。
- **只清理 registry**：active socket registry 指向不可用路径时，仍移除 stale `active.json` 记录，再扫描候选 socket。
- **回归测试**：更新扫描测试，验证失败候选路径会被保留；完整 Go 与扩展测试通过。
- **同步文档**：更新架构说明与 troubleshooting，明确 socket 路径不可被一次失败探测破坏。

### 🧠 Design Intent (Why)

此前 CLI 的 stale-socket 自愈逻辑把“连接失败”当成“socket 文件已失效”，直接 unlink 候选路径。Unix domain socket 的路径名与监听 inode 分离：对仍在运行的 listener unlink 后，旧进程可以继续存活并显示 native host connected，但后续 CLI 扫描永远找不到它。这正好解释了“扩展 connected、上一轮成功、下一轮 no connected target”的现象。修复后由 host 自己在退出时负责清理 socket，CLI 只修复自己的 registry 提示。

### 📁 Files Modified

- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/main_test.go`
- `docs/ARCHITECTURE.md`
- `skills/open-browser-use/references/troubleshooting.md`
- `docs/histories/2026-09/20260918-1115-preserve-native-host-socket.md`
