## [2026-08-26 15:30] | Task: 新增 wait_for_event:脚本侧消费 CDP 事件

### 🤖 Execution Context

- **Agent ID**: `pi-agent`
- **Base Model**: `claude`
- **Runtime**: `pi`

### 📥 User Query

> 回放一次 x.com 抓取 15 篇帖子的 OBU 会话(SPA 因 Cloudflare 门控永不
> 水合,agent 在 DOM 渲染链上消耗 20+ 轮),评估、并实现 P0.1:给 OBU 的
> heredoc 运行时加一个脚本侧 CDP 事件消费 API(最小实现:阻塞式
> `wait_for_event`),让 agent 能收 `Network.responseReceived` /
> `Runtime.consoleAPICalled` 等事件,而不是靠 DOM 轮询或注入拦截。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use/script_helpers.py`(go:embed 进 obu 二进制)、
配套单测、SKILL.md

**Key Actions:**

- **[wait_for_event]**: 阻塞等待指定 CDP 事件的 helper;签名
  `wait_for_event(method, predicate=None, timeout=30.0)`,返回匹配事件的内层
  params。事件管道原本全通(扩展 `onCDPEvent` → relay 广播 → prelude
  `_request` 静默丢弃),只缺消费端。实现:先排空 `_EVENT_QUEUE`,再以剩余
  时间片阻塞读 socket(临时调整 socket timeout,结束恢复);`predicate` 接收
  `{"source","method","params"}` dict。
- **[事件留存]**: `_request` 的读帧循环把无 id 通知帧(此前直接丢弃)入队
  `deque(maxlen=500)`,保证"别的 helper 调用期间到达的事件"不丢;有 id 的
  无关帧仍丢弃(保持原有响应关联语义不变)。
- **[自诊断错误]**: 超时抛错时列出已见事件 method 计数 top5;若目标是
  `Network.*` 且一个 Network 事件都没收到,附言提示先
  `cdp("Network.enable")` —— 让 LLM 一次失败即可自纠,而不是盲试。
- **[小重构]**: 连接建立逻辑从 `_request` 抽成 `_get_conn()`,`wait_for_event`
  复用(socket 惰性建立、共享超时)。
- **[单测]**: FakeHost 增 `push()`(等待期间从外部注入帧);新增 6 个用例:
  排队事件消费、predicate 过滤、等待期间到达(线程 + push)、超时诊断消息、
  参数校验、predicate 抛错上报。全套 29 个用例通过(`go test ./...`)。
- **[文档]**: SKILL.md 的 "Wait on conditions" 后新增 "Observe CDP events"
  小节 + 捕获 HomeTimeline 响应的 5 行示例(经 symlink 同步生效于
  `~/.pi/agent/skills/`)。

### 🧠 Design Intent (Why)

那次会话最大成本项是"站点特定逆向"(DOM 结构、虚拟滚动、__INITIAL_STATE__、
queryId),根源是 agent 只能看 DOM、不能看数据面。`wait_for_event` 把任务从
LLM 的弱先验区(新鲜站点知识)搬到强先验区(CDP 协议 + Playwright 式命名),
且是纯 Python 侧改动、Go/TS 零改动。"阻塞式"刻意最小:wire 线程模型、事件
背压、跨 run 持久注入(需要 `session_inject`)留作后续,不做进本次。

### 📁 Files Modified

- `cmd/open-browser-use/script_helpers.py`
- `cmd/open-browser-use/script_helpers_test.py`
- `skills/open-browser-use/SKILL.md`
- `docs/histories/2026-08/20260826-1530-wait-for-event-helper.md`（本文件）