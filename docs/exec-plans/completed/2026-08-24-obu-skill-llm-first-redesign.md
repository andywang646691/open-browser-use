# 执行计划:OBU Skill 与 CLI 接口的 LLM-first 重设计

> 本文件是一份**交给其他 agent 的任务提示词**,可以直接作为任务输入使用。
> 执行 agent 不需要了解本文件之外的历史讨论;所有决策和验收标准都在下面。

## 给执行 agent 的提示词(从这行开始复制)

你是一个执行 agent。你的任务是对 open-browser-use 的 skill 和 CLI 接口做一次
**LLM-first 重设计**:让 skill 大幅变短、让接口符合 LLM 的已知能力模式。

### 一、开始之前必读

- `AGENTS.md`、`docs/REPO_COLLAB_GUIDE.md`、`docs/ARCHITECTURE.md`(仓库协作与结构约定)
- `skills/open-browser-use/SKILL.md`(现状,180 行,本次改动的主要对象)
- `skills/open-browser-use/references/*.md`(三个已有参考文档)
- 参考对照:`/Users/zhengzheming/.agents/skills/browser-harness/SKILL.md`(瘦 skill 的范本,本次改动的设计来源)
- CLI 代码在 `cmd/open-browser-use/` 和 `internal/`(Go)

### 二、背景:现状的三个病根

1. **新词汇过多**:skill 发明了 `name-session`、`run_action_plan` 行式 DSL、`page-info`、`wait-load` 等训练语料里不存在的 token,模型需要现场学一本语法书,必然幻觉。
2. **嵌套字符串转义**:`--params '{"expression":"document.querySelector(\".foo\")"}'` 是 JSON + shell + JS 三层语言叠加,LLM 写这类转义的错误率极高。凡是要求模型手写"序列化后的文本"的接口都是错的。
3. **状态靠规则不靠默认值**:session 生命周期、tab 收尾、browser/profile 锁定全靠 prose 规则让模型"记住",skill 因此又长又容易违反。

### 三、五条设计准则(所有改动以此为准绳)

1. **熟词优先** — 接口词汇接地到 LLM 权重的分布:CDP 方法名(Accessibility.getFullAXTree、DOM.getBoxModel、Runtime.evaluate)、AX tree、Playwright 习语。不给熟悉语义起新名字。
2. **传值不传编码** — 模型永远不手写 JSON、shell 转义、URL 编码。带类型/结构化参数由运行时负责编码。编码是运行时的事,模型只负责语义。
3. **状态用默认值和环境变量** — 不靠模型"记住规则",靠默认行为安全。每命令携带的状态改为进程级环境变量,设置一次即忘。
4. **留逃生口** — raw CDP 直接可用,包装层随时可绕过,模型卡住时可以退回已知领域。
5. **知识按需加载** — skill 只承载 80% 主路径;20% 的边角知识(文件选择器、下载、剪贴板、claim 细则)移入 references,卡住才读。

### 四、必须实现的接口改动

#### 接口层(Go CLI,`cmd/open-browser-use/` + `internal/`)

1. **环境变量状态**:CLI 所有命令从环境变量读取默认值,显式 flag 仍可覆盖:
   - `OBU_SESSION_ID`(等价于现在的 `--session-id`)
   - `OBU_BROWSER` / `OBU_PROFILE`(等价于 `--browser` / `--profile`)
   现有 flag 保持兼容;缺省时 env 兜底。注意以真实用户 Chrome 为准,不要发明新的状态。

2. **finalize 默认安全**:`finalize-tabs` 缺省行为 = keep 空(关闭本任务所有 tab);只有显式 `--keep` 才保留。

3. **heredoc 脚本模式为主路径**:新增(或扩展现有机制,若已有类似入口则改造)一个 stdin 脚本模式,Python 风格、helper 预导入、返回值为可导航结构:

   ```bash
   obu <<'PY'
   tabs = user_tabs()
   claim_tab(tabs[0]["id"])
   open_tab("https://example.com")
   tree = cdp("Accessibility.getFullAXTree")["nodes"]
   box = cdp("DOM.getBoxModel", backendNodeId=n)["model"]["content"]
   print(evaluate("document.querySelector('h1').textContent"))
   finalize_tabs()
   PY
   ```

   - helper 命名:`user_tabs()`、`claim_tab(id)`、`open_tab(url)`、`navigate(tab_id, url)`、`tabs()`、`history(query)`、`cdp("Domain.method", **kwargs)`(kwargs 即参数,返回 dict)、`evaluate(js)`(Runtime.evaluate 的包装,返回值直接可用)、`finalize_tabs(keep=[...])`(缺省 keep 空)。
   - `cdp()` 接受结构化 kwargs,绝不要求 JSON 字符串参数。
   - heredoc 定界符固定 `<<'PY'`(带引号,防 shell 插值)。

4. **每次只保留一个"主力"路径**:raw CDP 是主工具;`navigate`/`open-tab`/`tabs`/`history` 是仅有的几个包装 helper。

#### Skill 层(`skills/open-browser-use/SKILL.md`)

重写为 ≤ 80 行,结构:

1. 简介(2-3 行:CDP 之上的薄 CLI,认识 DevTools 协议就等于认识它)
2. 一次性准备(`obu ping`;`obu profiles --connected`,多于一个目标就问用户)
3. 开始任务(export 三个环境变量,一次设置)
4. 浏览器内工作(先 `user_tabs()` 找可复用的已开 tab 再 `claim_tab`;然后 CDP 三原语:AX tree 找元素 → box model 取中心 → evaluate/点击;helper 一行带过)
5. 结束任务(`finalize_tabs()`,缺省 keep 空,每任务一次,之后不再调用浏览器工具)
6. Hard rules(安全底线,见"不可触碰的底线")
7. MCP / SDK(各 2-3 行;MCP 工具结构化参数由运行时编码,注明"不需要写转义";SDK 指向引用)
8. References(按需读取,不是提前读)

**Hard rules 块**(压缩但不丢失,参考 browser-harness 的写法):
- 操作的是用户真实 Chrome,绝不查看 cookie、密码、会话存储
- 上传、剪贴板、表单提交、购买、任何外部可见操作前先问
- 登录墙:停下问用户(已登录的 SSO 可直接用)
- 安装扩展、启动 Chrome 前先问
- 不猜 tab id,先列再操作

#### 内容迁移表(原 skill 章节 → 去向)

| 原内容 | 去向 |
|---|---|
| 每条命令的 `--session-id/--browser/--profile` 用法 | 环境变量一次设置,示例中清除 |
| Tab 生命周期章节(15+ 行,deliverable/handoff 分组) | 默认行为(finalize 缺省 keep 空)+ 新建 `references/tabs.md`(保留 claim 细则、分组语义、文件选择器、下载、剪贴板) |
| `run_action_plan` 行式 DSL 教学 | 删除教学;MCP 一节仅一句提及该工具存在 |
| `page-info` / `wait-load` 等包装词 | 删除,由 AX tree + `evaluate` 覆盖 |
| 浏览器/profile 选择 7 条规则 | 压缩为 3 行(列出目标 → 多于一个就问 → env 锁定,任务中途不切换) |
| 操作规则 10+ 条 | 压缩为 Hard rules 块 |
| 文件选择器/下载/剪贴板细节 | `references/tabs.md` |
| SDK/协议细节 | 不动,仍为 `references/sdk-and-protocol.md` |
| 安装、排障 | 不动,仍为 `references/installation.md` / `references/troubleshooting.md` |

#### 禁止出现的写法(全仓库 skill 示例中)

- 任何 `--params '{"..."}'` 形式的 JSON-in-shell
- 任何要求模型手写转义的示例
- 新造词汇(如新的行式 DSL、新的状态名)

#### 不可触碰的底线

- 安全规则只压缩不删减(见 Hard rules)
- session 概念保留,只是表达方式改为环境变量(扩展/native host 的 wire 协议不动)
- 多浏览器/profile 选择能力保留
- 不破坏与 `apps/chrome-extension`、`internal/host` 的现有协议兼容性
- MCP 工具集保持可用(可以改名以贴近 CDP,但要同步更新配置文档)

### 五、交付物

1. 重写后的 `skills/open-browser-use/SKILL.md`(≤ 80 行)
2. Go CLI 改动(env 兜底、finalize 默认、heredoc 脚本模式)+ 对应测试
3. 新建 `skills/open-browser-use/references/tabs.md`
4. 按 `docs/HISTORY_GUIDE.md` 在 `docs/histories/` 记录本次变更(包括迁移表摘要)
5. 检查部署副本 `~/.agents/skills/open-browser-use/` 是否需要同步,若需要则同步并在 history 里注明同步方式
6. 若 `docs/DESIGN.md` 或 `docs/ARCHITECTURE.md` 中有接口描述与本次改动冲突,顺手更新

### 六、验收标准(全部满足才算完成)

- [ ] `skills/open-browser-use/SKILL.md` ≤ 80 行
- [ ] skill 全文 grep 不到 `--params '{`、`--keep '["` 等 JSON-in-shell 模式
- [ ] skill 中所有带参示例均为 heredoc 脚本或结构化参数
- [ ] 五个"底线"逐条核对保留
- [ ] `go build ./...` 与 `go test ./...` 通过
- [ ] 手工冒烟:用 heredoc 脚本对本地 Chrome 跑通 open → AX → click/evaluate → finalize 全流程
- [ ] `OBU_SESSION_ID` 等 env 变量生效、旧 flag 仍兼容
- [ ] history 已按约定记录,决策留痕

### 七、风险与缓解

- 风险:改变主调用路径影响现有用户(CLI 用户、MCP 用户)。
  缓解:flag 全部保留兼容;heredoc 是新增路径不是替换;MCP 行为不变;变更记入 history 与 releases。
- 风险:finalize 默认值变化导致 tab 被意外关闭。
  缓解:默认"保留空"本来就是这个工具的既有推荐行为(原 skill 的 Keep no tabs by default),只是从规则变成默认值。
- 风险:skill 压缩时安全规则被顺手删掉。
  缓解:验收标准第 4 条逐条核对。

### 八、决策记录

- 2026-08-24:确立五条设计准则(熟词优先 / 传值不传编码 / 状态用默认值与 env / 留逃生口 / 知识按需加载),本次重设计以此为准绳。
- 2026-08-24:确定主调用形态为 `obu <<'PY' ... PY` heredoc + Python 风格 helper;CLI flag 保留兼容,env 兜底。