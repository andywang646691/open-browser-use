## [2026-09-12 19:52] | Task: Compress OBU startup into taskinit bootstrap

### 🤖 Execution Context

- **Agent ID**: `chatgpt`
- **Base Model**: `GPT-5.6 Sol`
- **Runtime**: `darwin-amd64`

### 📥 User Query

> 将每次 OBU 新任务的 `taskinit → list tabs → 查 site memory → 读 technique/runbook` 压缩成一步；tab 不要全量列出，只返回与当前 domain 匹配的 tab，并保留 ownership。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use`, `skills/open-browser-use`, `docs`

**Key Actions:**

- **[Taskinit Bootstrap]**: `obu taskinit <domain>/<slug>` 在创建 workspace/target 后直接输出 bootstrap context：仅保留 URL normalized host 与任务 domain 精确匹配的用户 tab，并携带 `ownerSessionId`；不自动 claim tab。
- **[Memory Hydration]**: 同一 bootstrap 自动读取匹配 `site-memory.lines` 原始行和这些行显式链接的 technique/runbook；site rows、单 artifact、artifact 总量都有输出预算，截断/缺失/非法链接会显式报告，runbook 只读不执行。
- **[Safety/Isolation]**: linked artifact 只允许安全 basename 与约定扩展名，拒绝 traversal、反斜杠路径和 symlink；`www.` 继续按既有 domain normalization 处理，但不会把其他 subdomain 当成匹配。
- **[Skill Flow]**: 将 Skill 的 Initialize + Load context 合并为 Bootstrap；新任务优先使用 taskinit 已给出的 matched tabs/memory，`user_tabs()` 与原生 grep/read 降级为无匹配、跨域、后续 turn fresh ownership、截断/缺失时的 fallback。
- **[TaskRef Exec]**: `taskinit` 现在显式输出 `taskRef=<domain>/<instance>`、task root 和绝对 `dir`；Agent 用 `dir` 写任务文件，用 `obu exec --task <taskRef> <file>` 从任意 caller cwd 执行，不再依赖 shell cwd。非默认 task root 可用 `--task-root`，旧 `obu exec <path>` 保持兼容。
- **[TaskRef Isolation]**: `--task` 只接受 task workspace 顶层普通文件名；taskRef、workspace 与脚本解析拒绝 traversal、目录逃逸和 symlink workspace/script，避免新的 locator 语法扩大路径边界。
- **[Verification]**: 新增 exact-host/ownership、memory linkage、unsafe ref、budget truncation、render output、relative exec、taskRef arbitrary-cwd/default-root/path-escape 回归测试；`make ci` 全绿并完成 `make local-install`。真机以临时 custom task root 跑 `taskinit localhost/taskref-smoke`，从 caller cwd `/` 用 `obu exec --task-root <root> --task <taskRef> 99_taskref_smoke.py` 成功加载该任务 session、以 workspace 为 Python cwd，并清理临时目录。

### 🧠 Design Intent (Why)

把确定性的启动检索从 LLM 决策层下沉到 CLI，减少重复 tool round-trips、漏读 memory 和无关用户 tab 上下文，同时保留 tab 选择/claim、跨域发现和 runbook 执行这些需要语义判断或显式动作的边界。

### 📁 Files Modified

- `cmd/open-browser-use/taskinit.go`
- `cmd/open-browser-use/bootstrap_context.go`
- `cmd/open-browser-use/bootstrap_context_test.go`
- `cmd/open-browser-use/script.go`
- `cmd/open-browser-use/script_test.go`
- `cmd/open-browser-use/taskref.go`
- `cmd/open-browser-use/taskref_test.go`
- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/memory.md`
- `docs/ARCHITECTURE.md`
- `docs/RELIABILITY.md`
- `docs/design-docs/site-memory.md`
- `docs/histories/2026-09/20260912-1952-taskinit-bootstrap-context.md`
