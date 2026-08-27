## [2026-08-28 00:28] | Task: 对齐 browser-harness 的 evaluate 调用语义

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `claude`
- **Runtime**: `go test + python3`

### 📥 User Query

> 让 OBU 的 `evaluate` 支持与 browser-harness `js()` 相同的调用方式。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use`（heredoc prelude 的 `evaluate(js)` helper）

**Key Actions:**

- **awaitPromise**: `Runtime.evaluate` 请求固定加 `awaitPromise: True`，async/异步 promise 表达式直接解出结果值。
- **顶层 return 自动包裹**: 首次原样求值失败且异常为 `Illegal return statement` 时，自动重试为 `(function(){...})()`；函数体内的 return 不受影响（不会误包裹）。
- **unserializableValue 解码**: `BigInt`(`42n`)、`NaN`、`±Infinity`、`-0` 从 CDP 字符串解码为对应 Python 值。
- **异常信息增强**: 从 `exceptionDetails` 提取 JS 异常 description，带行/列号，保留 `evaluate threw` 前缀保持兼容。
- **超时友好报错**: socket 超时转为带表达式 snippet 的 `RuntimeError`。
- 以上语义与 `src/browser_harness/helpers.py` 的 `js()` 一致（契约由 docstring 承载）。

### 🧠 Design Intent (Why)

browser-harness 已验证过这套调用方式对 Agent 更友好：`return` 语句无需手动包函数、promise 直接 await、可读异常定位。OBU prelude 作为 Agent 主要交互路径，直接复用成熟语义而非发明新语法；异常消息保留 `evaluate threw` 前缀避免破坏既有脚本/模型对报错的预期。

### 📁 Files Modified

- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`（FakeHost 支持 tuple 响应队列；新增 4 个 evaluate 用例）
