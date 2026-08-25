## [2026-08-24 12:30] | Task: 本地 obu 一键构建脚本

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 后续改了代码，如何快速测试？（此前已将全局 obu 替换为本地构建版本）

### 🛠 Changes Overview

**Scope:** scripts/ + Makefile

**Key Actions:**

- 新增 `scripts/install-local.sh`：`go build` 当前源码到 `~/.local/bin/open-browser-use`，重建 `obu` symlink，并把 Chrome native host stable link（macOS/Linux）指向本地二进制；最后自验证版本号与 `mcp` 未暴露。
- `Makefile` 新增 `local-install` target（`.PHONY` 同步更新）。

### 🧠 Design Intent (Why)

把"改代码 → 重新构建 → 替换全局入口 → 验证"收敛成一条命令，避免每次手敲
`go build -o ~/.local/bin/...`。恢复全局 npm 版本只需删掉 `~/.local/bin`
下两个文件并把 stable link 指回 npm 包内二进制。

### 📁 Files Modified

- `scripts/install-local.sh`（新增）
- `Makefile`