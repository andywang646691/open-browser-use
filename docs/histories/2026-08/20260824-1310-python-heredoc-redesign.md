## [2026-08-24 13:10] | Task: heredoc 重写为真 Python 执行器

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 重设计 heredoc 脚本模式（原自写 Python 子集让 LLM 系统性地语法幻觉）。
> 参考 browser-harness 方案：不发明语法；API 契约由 docstring 承载。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use/`（CLI 脚本模式）、skill、架构文档

**Key Actions:**

- **[执行引擎]**: `script.go` 从 1338 行自写 lexer/parser 重写为 ~150 行
  执行器：管道 stdin → spawn 系统 `python3 -u -`，执行 go:embed 的
  prelude + 用户脚本；stdout/stderr 原样透传；python 退出码透传
  （`scriptExitError`，`main()` 按码退出）。socket 由 Go 侧复用现有
  registry/profile 路由解析，经 `OBU_SCRIPT_SOCKET` 传给 prelude；
  解析失败延迟到 helper 调用时才报（纯 Python 脚本不受影响）。
- **[prelude]**: 新增 `cmd/open-browser-use/script_helpers.py`（go:embed，
  也是 Python 单测直接对象）：标准库实现 Browser Use JSON-RPC frame
  编解码（4 字节 native-endian + JSON，对齐 `internal/wire`）；10 个
  helper（user_tabs/tabs/claim_tab/open_tab/navigate/history/name_session/
  cdp/evaluate/finalize_tabs），每个带工程化 docstring（签名 + 返回结构 +
  例子 + 边界行为）；`_help()` 批量速查。
- **[测试]**: `script_helpers_test.py`（临时 socket 假 server，11 用例，
  frame 编解码/调用序列/解包/错误路径）；`script_test.go` 重写（lexer/
  parser 测试退役，新增真 Python 集成：print/推导/if/exit code/traceback
  透传/docstring 自省/env 传递）。
- **[skill/文档]**: SKILL.md heredoc 段声明"真 Python + help(name) 自省"；
  ARCHITECTURE heredoc 段落重写。

### 🧠 Design Intent (Why)

自写子集让 LLM 系统性越界（if/比较/f-string/推导实测全报错且报错不指
方向）。真 Python 消灭语法层摩擦；docstring 承载 API 契约，随代码走
不过期，`help()`/`dir()` 是 LLM 零成本自省（browser-harness 同款设计）。
状态保持不引入 daemon：跨调用 tab 状态本就在扩展 session，python 变量
单进程内有效即可。

### 📁 Files Modified

- `cmd/open-browser-use/script.go`（重写）
- `cmd/open-browser-use/script_helpers.py`（新增）
- `cmd/open-browser-use/script_helpers_test.py`（新增）
- `cmd/open-browser-use/script_test.go`（重写）
- `cmd/open-browser-use/main.go`（exit code 透传 + 调用点）
- `skills/open-browser-use/SKILL.md`
- `docs/ARCHITECTURE.md`
- `docs/exec-plans/active/2026-08-24-python-heredoc-redesign.md`（进度）