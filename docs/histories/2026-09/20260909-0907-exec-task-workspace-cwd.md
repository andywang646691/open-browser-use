## [2026-09-09 09:07] | Task: 让 obu exec 使用任务工作区 cwd

### 🤖 Execution Context

- **Agent ID**: `OpenAI coding assistant`
- **Base Model**: `当前 API 未提供可靠的具体型号标识`
- **Runtime**: `pi coding agent`

### 📥 User Query

> 修改：`obu exec` 应当自动把 cwd 切换到任务目录，因为任务目录是 task workspace。

### 🛠 Changes Overview

**Scope:** Go CLI Python 文件执行、测试、运行时文档与 OBU skill。

- `obu exec <file.py>` 先规范化脚本绝对路径，再将 Python 子进程的 cwd 设置为脚本所在目录。
- 增加回归测试，验证相对路径执行上下文切换到任务目录，同时调用方进程 cwd 不变。
- 更新架构、稳定性文档和 OBU skill，明确 task workspace 是脚本执行 cwd。

### 🧠 Design Intent (Why)

任务目录是脚本、同目录模块、`.obu.env` 和失败尝试的统一工作空间。让子进程从该目录启动后，脚本可以自然使用相对路径读写任务产物；仅修改子进程 cwd，不改变调用 OBU 的父进程环境。

### 📁 Files Modified

- `cmd/open-browser-use/script.go`
- `cmd/open-browser-use/script_runner.py`
- `cmd/open-browser-use/script_test.go`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `skills/open-browser-use/SKILL.md`
