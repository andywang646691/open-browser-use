## [2026-08-24 11:49] | Task: OBU Skill 与 CLI 接口 LLM-first 重设计

### 🤖 Execution Context

- **Agent ID**: pi (coding agent)
- **Base Model**: claude (当前会话模型)
- **Runtime**: pi harness, macOS

### 📥 User Query

> 执行最新的执行计划(docs/exec-plans/active/2026-08-24-obu-skill-llm-first-redesign.md):对 open-browser-use 的 skill 和 CLI 接口做 LLM-first 重设计 —— skill 大幅变短、接口符合 LLM 已知能力模式(熟词优先、传值不传编码、状态用 env 默认值、留逃生口、知识按需加载)。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use`(Go CLI),`skills/open-browser-use`(skill + references),`docs/`

**Key Actions:**

- **heredoc 脚本模式**:无参数 `obu` 且 stdin 为管道时进入 Python 风格脚本模式;新文件 `script.go` 实现词法/解析/求值子集(spf13/cobra + 现有 socket 层之上),helper 预导入:`user_tabs()`、`tabs()`、`claim_tab(id)`、`open_tab(url)`、`navigate(tab_id, url)`、`history(query, limit=)`、`name_session(name)`、`cdp("Domain.method", **kwargs)`、`evaluate(js)`、`finalize_tabs(keep=[...])`、`print(...)`、`len(...)`;支持字面量/列表/dict、下标与切片 `[0:5]`、元组解包赋值、算术运算(`+ - * / // %`、一元负号、括号)、`#` 注释、`;` 分隔;产生值逐行 JSON 输出;`open_tab`/`claim_tab` 设置当前 tab 供 `cdp`/`evaluate` 复用;`evaluate` 解包 CDP `Runtime.evaluate` 的 RemoteObject 返回可直接用的值。
- **env 状态兜底**:`addSocketFlags` 在 flag 缺省时读 `OBU_SESSION_ID` / `OBU_BROWSER` / `OBU_PROFILE` 作为默认值,flag 仍优先;MCP server 同样读取 `OBU_SESSION_ID`;root 命令也挂上 socket flags 以便 `obu --browser x <<'PY'`。
- **finalize 默认安全**:`finalize-tabs` 与 `finalize_tabs()` 缺省 keep 为空(原本就是推荐行为,现在脚本 helper 直接缺省)。
- **skill 重写**:`skills/open-browser-use/SKILL.md` 从 180+ 行压到 78 行:简介 → 一次性准备(`obu ping` / `obu profiles --connected`) → env 开始任务 → heredoc 主路径 + CDP 三原语(AX tree → box model → evaluate 驱动交互) → `finalize_tabs()` 收尾 → Hard rules(只压缩不删减) → MCP/SDK → References;移除全部 JSON-in-shell 示例与新造词汇教学(`run_action_plan` 仅一句提及)。
- **内容迁移**:tab 生命周期、claim 细则、deliverable/handoff、文件选择器、下载、剪贴板迁入新 `references/tabs.md`;`sdk-and-protocol.md` 中的 `--params '{...}'` / `--keep '[]'` 示例替换为脚本模式等价写法。

### 🧠 Design Intent (Why)

原 skill 的三个病根:新词汇过多(模型幻觉来源)、嵌套字符串转义(JSON+shell+JS 三层叠加,模型错误率高)、状态靠 prose 规则(又长又容易违反)。五条准则落地方式:CDP 方法名/AX tree 等熟词直接可见;运行时负责所有编码,模型只传语义;状态从"记住规则"变成 env 默认值;raw CDP 全量可用作逃生口;80% 主路径在 skill,20% 边角知识进 references 按需读。

### 📁 Files Modified

- `cmd/open-browser-use/script.go`(新增:脚本模式解释器)
- `cmd/open-browser-use/script_test.go`(新增:18 个测试,含 fake invoke 协议断言)
- `cmd/open-browser-use/main.go`(env 兜底、root heredoc 分发、版本 0.1.42)
- `cmd/open-browser-use/mcp.go`(OBU_SESSION_ID 兜底)
- `skills/open-browser-use/SKILL.md`(重写,76 行)
- `skills/open-browser-use/references/tabs.md`(新增)
- `skills/open-browser-use/references/sdk-and-protocol.md`(去 JSON-in-shell 示例)
- `docs/ARCHITECTURE.md`(补 script 模式与 env 描述)
- `docs/releases/feature-release-notes.md`(2026-08 条目)

### ✅ 验收核对

- [x] SKILL.md ≤ 80 行(78 行)
- [x] skill 目录 grep 不到 `--params '{` / `--keep '["`
- [x] 带参示例均为 heredoc 脚本/结构化参数
- [x] 五条底线逐条保留(安全规则只压缩不删减;session 概念保留为 env;多 browser/profile 能力保留;wire 协议未动;MCP 工具集仍可用)
- [x] `go build ./...` 与 `go test ./...` 通过(新增 20+ 测试)
- [x] 手工冒烟:对本地真实 Chrome 用 heredoc 跑通 user_tabs → open_tab → AX tree → box model → evaluate → finalize_tabs 全流程,示例逐行验证
- [x] `OBU_SESSION_ID`/`OBU_BROWSER`/`OBU_PROFILE` 生效,旧 flag 兼容(实测 env 与 flag 覆盖)
- [x] history 已记录

### 🔎 冒烟中的关键发现(已沉淀进 skill)

- 真机验证时发现通过扩展 debugger 对**后台 tab** 派发 `Input.dispatchMouseEvent` 事件**不会到达页面**(mousemove 计数为 0、activeElement 不变、偶发 socket 超时);JS 点击(`el.click()`)稳定可靠。skill 因此把 `evaluate(js)` 作为默认交互路径,`Input.dispatchMouseEvent` 保留为 raw CDP 逃生口。
- `DOM.getBoxModel` 的 backendNodeId 必须以 `DOM.getDocument` 先行建立 DOM 快照后才与 AX tree 的 `backendDOMNodeId` 对齐;页面未加载完时取 AX 快照会出现 id 偏移。skill 的坐标流程已包含 `cdp("DOM.getDocument")` 一步。
- 这两条都是既有栈的真实行为(扩展从未实现 click 路径,参考客户端是前瞻实现),不是本次改动的回归。

### 📤 部署副本同步

- `~/.agents/skills/open-browser-use/` 为部署副本(此前与仓库内容一致)。本次以 `rsync -a --delete --exclude '.DS_Store'` 方式同步:SKILL.md、references/*(新增 tabs.md)全量覆盖,`agents/openai.yaml` 保留。同步方式已记录于此,后续再改 skill 记得同步。