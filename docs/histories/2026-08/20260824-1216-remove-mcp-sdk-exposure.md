## [2026-08-24 12:16] | Task: 下线 MCP 与 SDK 对外模式（源码保留）

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 删掉对外暴露的 mcp 和 sdk 模式，但是不要删掉源码。

### 🛠 Changes Overview

**Scope:** CLI（Go）、GitHub Actions 发布流水线、README（EN/zh-CN）、skill 包、架构/CI/CD/供应链文档

**Key Actions:**

- **[MCP 下线]**: `newMCPCommand()` 从 CLI 根命令注册中移除，`obu mcp` / `open-browser-use mcp` 不再可用；`cmd/open-browser-use/mcp.go` 源码与 `script_test.go` 中对应测试原样保留，`go test` 全绿。
- **[SDK 发布下线]**: `npm-publish.yml` 删除 `publish-npm-sdk` job（保留 CLI npm 发布）；删除整个 `pypi-publish.yml` workflow。JS/Python/Go SDK 源码包保留在 `packages/`，内部测试链路（`pnpm -r test`、python unittest）不动。
- **[用户面去宣传]**: 两个 README 删除 SDK badge、SDK 章节和 MCP 章节；CLI npm 包 README 删除 MCP Server 章节与 SDK 对比文案。
- **[skill 去宣传]**: `SKILL.md` 删除 "MCP / SDKs" 章节、frontmatter description 去掉 SDK 提及、References 去掉 sdk-and-protocol 链接；`references/installation.md` 删除 SDKs 组件说明；删除 `skills/open-browser-use/references/sdk-and-protocol.md`（skill 包内唯一的 SDK/MCP 用法指南，随 skill 对外分发）。
- **[内部文档同步]**: `docs/ARCHITECTURE.md` 重写 SDK/MCP 段落为"源码保留、不再对外暴露"；`docs/CICD.md`、`docs/SUPPLY_CHAIN_SECURITY.md`、`docs/QUALITY_SCORE.md` 更新发布链路描述；active exec plan 增加下线记录。

### 🧠 Design Intent (Why)

对外暴露面 = CLI 命令注册 + npm/PyPI 发布流水线 + README/skill 中的使用指引。
三个层面全部移除，但实现源码（mcp.go、三个 SDK 包及其测试）保留在仓库内并
继续随 CI 构建测试，未来需要恢复时只需重新注册命令、重新开启发布流水线，
不需要找回代码。协议 socket 与 JSON-RPC 能力不受影响（CLI 自身仍在使用）。

### 📁 Files Modified

- `cmd/open-browser-use/main.go`
- `.github/workflows/npm-publish.yml`
- `.github/workflows/pypi-publish.yml`（删除）
- `README.md`
- `README.zh-CN.md`
- `packages/open-browser-use-cli/README.md`
- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/installation.md`
- `skills/open-browser-use/references/sdk-and-protocol.md`（删除）
- `docs/ARCHITECTURE.md`
- `docs/CICD.md`
- `docs/SUPPLY_CHAIN_SECURITY.md`
- `docs/QUALITY_SCORE.md`
- `docs/exec-plans/active/2026-05-12-chrome-profile-selection.md`