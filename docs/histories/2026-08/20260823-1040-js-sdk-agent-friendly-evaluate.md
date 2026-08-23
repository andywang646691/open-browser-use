## [2026-08-23 10:40] | Task: JS SDK Agent-friendly evaluate/waitFor/finalize 改进

### 🤖 Execution Context

- **Agent ID**: pi
- **Base Model**: claude
- **Runtime**: bun + pnpm workspace

### 📥 User Query

> 基于一次真实浏览器任务(读取 x.com 信息流、验证登录态、清理孤儿会话)踩到的坑,
> 改进 open-browser-use 源码:LLM 写 SDK 代码时最容易出错的部分是 evaluate 表达式转义、
> 错误信息缺真实堆栈、finalizeTabs keep 参数格式不透明、debugger detach 后裸报错。

### 🛠 Changes Overview

**Scope:** `packages/open-browser-use-js`(SDK)、`apps/chrome-extension`(错误信息)、SDK 测试与 README

**Key Actions:**

- **evaluate 函数优先签名**: `tab.evaluate(pageFunction, args?)` 内部经
  `serializePageFunction` 序列化(函数 `toString()` + JSON args),消灭多层字符串转义;
  字符串形式保留并显式化为 `evaluateRaw`。
- **完整错误面**: 页面异常抛 `OpenBrowserUseEvaluateError`,消息取自 CDP
  `exceptionDetails.exception.description`(真实错误+堆栈),附带 `.expression`/`.tabId`;
  新增 `formatEvaluateError` 纯函数。
- **waitFor 原语**: `tab.waitFor(selector | pageFunction, {timeoutMs, intervalMs})`
  轮询等待,超时抛 `OpenBrowserUseWaitTimeoutError` 并携带 `.lastValue`。
- **finalizeTabs 类型与校验**: keep 参数类型收紧为
  `{tabId, status: "handoff" | "deliverable"}[]`,本地 `assertFinalizeKeep` 前置校验,
  错误信息直接列出合法值与格式;扩展端三条校验错误同步带上合法值提示。
- **debugger detach 自愈**: `call()` 捕获 "Debugger unattached" 后重置 attach
  记账状态并重试一次(finally/finalize 后 debugger 被 detach 的场景)。
- **executeCdp/call 返回类型**: 标注为 `CdpCommandResult`(CDP 响应本体),
  文档化 `.result.value` / `.data` 等取值形状。

### 🧠 Design Intent (Why)

*实战证明 LLM 在多层字符串转义(evaluate 表达式)和远端校验错误上失败率最高。*
*把序列化收进 SDK(make the caller express intent, not escaping)、把真实堆栈带回错误、*
*把 keep 格式和等待语义变成自解释 API,是三个最高杠杆的改动。函数签名镜像*
*Playwright 的 `evaluate(fn, arg)`,字符串形式降级为逃生口 `evaluateRaw`。*

### 📁 Files Modified

- `packages/open-browser-use-js/src/index.ts`
- `packages/open-browser-use-js/test/frame.test.mjs`(新增 8 个测试,13/13 通过)
- `packages/open-browser-use-js/README.md`
- `apps/chrome-extension/background.js`(finalizeTabs 校验错误信息)
- `docs/histories/2026-08/20260823-1040-js-sdk-agent-friendly-evaluate.md`

### ⏭ Follow-ups

- Go / Python SDK 同步 finalizeTabs 类型与错误信息
- 提供 `endSession` / 会话枚举 API,解决 handoff 后会话长期驻留的问题
- CLI 的 `--params` 支持 `@file`,行式计划增加 `eval` 动作
