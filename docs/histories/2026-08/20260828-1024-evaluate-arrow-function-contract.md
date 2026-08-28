## [2026-08-28 10:24] | Task: 明确 evaluate(js) 不对函数值自动调用

### 🤖 Execution Context

- **Agent ID**: `pi`
- **Base Model**: `claude`
- **Runtime**: `python3 -m unittest script_helpers_test`

### 📥 User Query

> 为什么 LLM 总在 `evaluate(js)` 上犯错：`evaluate("document.querySelectorAll('article').length")`
> 能用，而把 `"() => {...}"` 箭头函数字符串传进去却行为异常？

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use`（heredoc prelude 的 `evaluate(js)` helper）

**Key Actions:**

- **契约写入 docstring**: 明确 snippet 原样求值、**没有自动调用**——裸函数值
  （`"() => ({a: 1})"`、`"async () => ..."`）不可 JSON 序列化，静默返回 `None`
  且函数体根本不执行；要跑函数体需在 snippet 内自调用
  `"(() => {...})()"` / `"(async () => ...)()"`（promise 由 awaitPromise 自动化解）。
- **修正误导示例**: 原 docstring 示例 `"async () => await fetch(...)"` 本身就是
  错误的用法（裸 async 箭头函数也是函数值 → `None`），LLM 读契约就会照抄犯错。
- **回归测试**: 新增 2 个用例，钉死"裸箭头函数 → None 且只发一次请求、不做
  illegal-return 包裹重试；IIFE 与 async IIFE 正常返回值"的行为。

### 🧠 Design Intent (Why)

根因：`evaluate` 把字符串原样交给 CDP `Runtime.evaluate`（`returnByValue: true,
awaitPromise: true`），而 CDP 对"表达式求值为函数"既不调用也不序列化，返回的
RemoteObject 没有 `value`，helper 静默转成 `None`。LLM 的箭头函数习惯来自
Playwright/browser-use 生态（`page.evaluate(fn)` 会执行函数、字符串表达式求值为
函数时也会被自动调用），迁移到裸 CDP 语义后失败是静默的，模型无法从报错学习，
只能靠猜。把"无自动调用 + 必须 IIFE 自调用"写进契约并加测试，让模型在
`help(evaluate)` 阶段就拿到正确用法。

### 📁 Files Modified

- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`