## [2026-08-25 00:03] | Task: 修复 prelude 响应/通知帧错位

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> （来自上一条调试对话）`obu <<'PY'` 脚本在 x.com 场景报
> `TypeError: 'NoneType' object is not subscriptable`（stdin line 259）。
> 确认根因后执行修复方案 1：prelude `_request` 按请求 id 关联响应、
> 跳过通知帧。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use/script_helpers.py`（heredoc prelude）

**Key Actions:**

- **[根因]**: 行号对不上是因为 `script.go` 把 252 行内嵌 prelude 拼在用户
  脚本前，用户第 6 行 `tree = cdp("Accessibility.getFullAXTree")["nodes"]`
  落在 stdin 259 行（6.6s 总耗时也证明死在 sleep 之后）。真正的 bug：
  扩展把每个 CDP 事件转发成无 id 的 `onCDPEvent` 通知（另有 heartbeat
  通知），relay 把它们广播给所有客户端，而 prelude `_request` 每请求只
  读一帧、不校验 id → 读到的常是通知帧 → `body.get("result")` 返回 None
  → `None["nodes"]` 抛 TypeError。Python SDK 的 `request()` 早已有
  id 关联循环，prelude 漏了同一保护。
- **[修复]**: `_request` 请求 id 改为自增唯一字符串 `obu-script-N`
  （保持字符串，relay 依赖 string id 做映射）；发送后循环读帧，`id`
  不匹配（通知 / 其他帧）则跳过，直到拿到本请求的响应；错误/结果处理不变。
- **[测试]**: `script_helpers_test.py` 假 server 支持在响应前注入
  `extra_frames`；补 4 用例：通知帧跳过（onCDPEvent + heartbeat）、
  错位响应 id 跳过、请求 id 唯一且为字符串、x.com 回归
  （open_tab → 事件插入 → cdp AX tree 正常解包）。15 用例全绿，
  `go test ./...` 全绿。
- **[文档]**: troubleshooting 新增「heredoc 报 NoneType」条目（含过期
  二进制提示）；heredoc exec plan 进度/决策追加本次修复记录。

### 🧠 Design Intent (Why)

wire 协议和 relay 本来就用请求 id 关联（relay 对 string id 做 pending
映射、恢复原 id），prelude 侧补齐同一层保护最直接、不触碰协议；跳过
而非报错是因为 relay 对 script 客户端只会发「本请求的响应」和「广播
通知」两类帧，通知理应被丢弃。与 Python SDK 的既有行为对齐，同类问题
只修一次。

### 📁 Files Modified

- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `skills/open-browser-use/references/troubleshooting.md`
- `docs/exec-plans/active/2026-08-24-python-heredoc-redesign.md`