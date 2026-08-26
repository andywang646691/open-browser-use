## [2026-08-26 10:04] | Task: profiles 检测补上 fork unpacked 扩展(修复探测误判)

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 其他 agent 排障反馈:OBU 实际连的是 Default profile(fork 版扩展
> ldnahhefbkmhjohgiamfnfppaooejmag,Load unpacked),而 `obu profiles --connected`
> 只列出 Profile 1(test,商店版 0.1.41),扫描看不到 unpacked 安装,导致探测
> 结果误判连接目标。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use/main.go`、检测单测、ARCHITECTURE

**Key Actions:**

- **[候选 id]**: `chromeExtensionCandidateIDs()` 增加本 fork 的
  `forkExtensionID`(常量,与仓库 manifest `key` 派生的 id 一致),并支持
  `OBU_EXTENSION_IDS` 环境变量注入额外候选 id(逗号分隔、去重;fork 换 key
  后无需改代码)。此前候选列表只有商店版 + beta 版 id,unpacked 检测路径
  (`Secure Preferences` 的 `extensions.settings[id].path`)其实早已存在,但
  从来没拿着 fork id 去查过。
- **[单测]**: `TestChromeExtensionCandidateIDsIncludesForkAndEnv` —— 默认商店
  id 打头、fork id 存在、env 注入生效、重复去重。
- **[验证]**: `make local-install` 后 `obu profiles --connected` 从只列
  `Profile 1 test 0.1.41 -` 变为正确列出 `Default 用户2 0.1.42 yes`;
  磁盘核验:fork id 只在 Default 的 `Local Extension Settings/`,商店版
  `bgjoihaep…` 只在 Profile 1 的 `Extensions/`。
- **[文档]**: ARCHITECTURE 的 profile 检测段补充 fork 候选 + Secure
  Preferences unpacked 发现机制。

### 🧠 Design Intent (Why)

探测列表被误读为"连接目标"源自真实盲区:fork 是 Load unpacked,不产生
`Extensions/<id>` 目录,而候选列表写死上游 id。把 fork id 纳入候选
(加上 env 逃生口)后,已有的 unpacked 检测路径立刻生效,`profiles` 如实
反映各 profile 的扩展情况,避免 agent/用户被误导。

### 📁 Files Modified

- `cmd/open-browser-use/main.go`
- `cmd/open-browser-use/main_test.go`
- `docs/ARCHITECTURE.md`
- `docs/histories/2026-08/20260826-1004-fork-profile-detection.md`（本文件）