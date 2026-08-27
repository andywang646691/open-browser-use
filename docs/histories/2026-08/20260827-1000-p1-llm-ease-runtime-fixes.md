## [2026-08-27 10:00] | Task: P1 落地 —— 让 LLM 更容易正确调用 OBU 运行时

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `opencode/deepseek-v4-flash`
- **Runtime**: `pi`

### 📥 User Query

> 复盘一次 x.com 时间线抓取任务(OBU + 隐藏窗口 + HomeTimeline GraphQL),
> 发现 6 处执行摩擦,其中 4 处属于"契约不可见 + 编排放手"的可避免问题。
> 用户要求:将 P1 优化项全部落地 —— ① wait_for_event 超时诊断升级
> (列 URL 样本,让"谓词过窄/匹配到 JS bundle"一目了然);
> ② memory_put 失败信息可操作化(标注合法字段、接受常见别名);
> ③ SKILL.md 中 wait_for_event 示例消除"返回值再取 ev['params']"的传染源。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use/`(python prelude + 测试)、
`skills/open-browser-use/`(SKILL.md + references/memory.md)

**Key Actions:**

- **[wait_for_event 超时诊断]**: `_count` 现在为 `Network.responseReceived`
  事件记录最近 5 条响应 URL(`seen_urls`);超时抛错新增
  `; Network.responseReceived URLs: ...`(最多 3 条、单条截断到 160 字符),
  与已有的方法名统计、`Network.enable` 提示并列。谓词过窄/事件流里混着 JS
  bundle 这类"请求在飞但没匹配上"的失败,不再表现为毫无信号。
- **[memory_put 宽容化]**: 签名改为 `note=None, **kwargs`,接受别名
  `subject`/`body` → `note`、`how` → `unlock`(两者同时给时拼接);未知 kwarg
  抛 `TypeError`,消息列出合法字段 `note / unlock / selector / tech / runbook`
  与别名映射;`kind='runbook'` 的错误专门提示 "runbook 是字段不是 kind,
  请用 runbook='<name>.py' + kind='tip'"。所有校验仍在写文件前完成。
- **[示例去传染源]**: `script_helpers.py` 的 wait_for_event docstring 示例与
  `skills/open-browser-use/SKILL.md` 示例同步改为变量区分 —— 谓词形参
  `event`(收到完整 event,取 `event["params"]["response"]`)、返回值
  `params`(直接取 `params["requestId"]`),并加一行显式注释点破两者形状不同。
- **[文档]**: `references/memory.md` 字段表后新增别名段落。
- **[测试]**: 新增 4 个单元测试 —— 超时消息含 URL 样本(含 bundle 与
  GraphQL 两类 URL)、`subject`/`body`/`how` 别名写入正确、
  未知 kwarg 报错列出合法字段、`kind='runbook'` 提示指向字段。

### 🧠 Design Intent (Why)

上一轮复盘确认:在 TTUI 折叠输出下,"运行期 docstring 是唯一权威"的假设
不成立 —— 关键契约信息常被截断。本次改动遵循"**失败信号必须自带修复
所需信息**"的原则:超时诊断给出事件流里的 URL 样本(而非只有方法名与计数),
参数误用给出字段清单并直接宽容处理常见猜错(kwarg 别名),示例代码从源头
消除命名歧义。让下一次 LLM 在同样场景下从"猜 + 试错"变为"报错即答案"。

### 📁 Files Modified

- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `skills/open-browser-use/SKILL.md`
- `skills/open-browser-use/references/memory.md`
- `docs/histories/2026-08/20260827-1000-p1-llm-ease-runtime-fixes.md`(本文件)

### 🔬 Validation

- `python3 cmd/open-browser-use/script_helpers_test.py` → 37 tests OK
- `go test ./cmd/open-browser-use/...` → ok(内嵌新版 script_helpers.py)
- `make ci` → 全绿(基础 CI 检查通过)
- 真二进制端到端(`./scripts/install-local.sh` 重装后,`OBU_MEMORY_DIR`
  隔离验证):`subject=`/`body=` 写入成功;未知 kwarg 报错列出字段;
  `kind='runbook'` 提示指向字段 —— 3 项均符合预期