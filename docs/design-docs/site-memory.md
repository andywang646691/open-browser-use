# 站点经验沉淀 (Site Memory) 设计

**状态**: 已定稿，待实现
**摘要**: 把每次 OBU 访问页面的经验沉淀为按 domain 索引的原子数据层，
供下一次 Agent 复用；skill 数量永久锁定为 1，经验增长只发生在数据，
永不产生站点级 skill。
**关联**: `skills/open-browser-use/`（skill 集成）、
`cmd/open-browser-use/script_helpers.py`（唯一的写 helper）

---

## 1. 问题与目标

现状：每次 OBU 会话的页面经验（登录怎么进、坑在哪、怎么验证）只存在于
对话上下文里，下一轮、下一个 Agent 全部重来。

目标：

- **沉淀**：每次访问产生的「贵的知识」能低成本落盘。
- **复用**：下一次 Agent 访问同一 domain 前能按需命中。
- **不爆炸**：无论积累多少站点，skill 数量不变。

## 2. 核心原则

> **技能面固定，经验只长在数据层。**

Skill 是每次任务都要加载的接口，每新增一个都会加重所有后续任务的
上下文负担——这是爆炸的根源。因此：

- skill 数量永久锁定为 1（现有的 `open-browser-use`）。
- 经验增长只发生在按 domain 索引的数据文件里，按需读取。
- 任何「站点级 skill / 站点级目录类型」都是违规项。
- **记忆是纯文件：读用 agent 原生工具（grep/find/read），不新增任何
  CLI 命令层**。写只有一处极小 helper，理由见 §5.2。

## 3. 总体架构

```text
Agent
  │  读：原生工具 grep / find / read（按 memory.md 的约定路径）
  │  写：memory_put(...) prelude helper（仅 OBU heredoc 任务内）
  ▼
~/.open-browser-use/memory/          ← 默认存储；OBU_MEMORY_DIR 可覆盖
  README.md                          规则自描述（没加载 skill 的 agent 也会学会怎么写）
  site-memory.lines                  一级：原子经验行（唯一的写入入口）
  techniques/<name>.md              跨域技术经验（虚拟滚动、后台节流、网络兜底…）
  playbooks/<domain>.md              二级：热门域升级文档（手动聚合生成）
  runbooks/<name>.py                 三级：跑通的代码，一键复跑（obu < 文件）
```

复用是**按 domain 按需读取**：上下文成本为 O(命中的 domain)，与历史总量无关。

## 4. 存储与数据格式

### 一级：`site-memory.lines`（唯一的写入入口）

每行一条原子记录，**字段全部带标签、管道分隔**——`|` 在字段内禁用，
因此 LLM 用手边的文件工具手动 append 也安全，无需 JSON 转义：

```text
domain=x.com|ts=2026-08-25T10:00:00Z|kind=surprise|note=时间线虚拟滚动, DOM 只渲染可见项+缓冲|unlock=改抓网络请求|tech=virtual-scroll-throttle
domain=github.com|ts=2026-08-24T10:00:00Z|kind=fail|note=SSO 第二次点击被重定向回首页|unlock=等 3s 再点
domain=twitter.com|ts=2026-08-24T10:05:00Z|kind=surprise|note=登录弹窗是 iframe, AX 树拿不到|unlock=先切 frame|selector=iframe[title="login"]
domain=github.com|ts=2026-08-24T10:10:00Z|kind=tip|note=导出入口在右上菜单 → Settings → Billing
```

字段（均可缺省，除 `domain`/`kind`/`note`）：

- `domain`: 唯一键，一跳子域归并到主域（如 `www.github.com` → `github.com`）
- `ts`: 日期或 ISO 8601 时间（用于 stale 判定）
- `kind`: `fail`（踩坑）/ `surprise`（意外结构）/ `tip`（正面套路，少用）
- `note`: 一个可操作事实
- `unlock`: 解锁动作（`fail`/`surprise` 必填；`tip` 可省）
- `selector`（可选）: 验证过的 selector，便于复用前重放验证
- `tech`（可选）: 指向跨域技术文件 `techniques/<name>.md`（见「技术层」）
- `runbook`（可选）: 指向可复用脚本 `runbooks/<name>.py`（见 §4.3）

**语法不依赖 LLM 先验**：字段用全称（`domain=` 而非 `d=`），形状对齐
常见的 `label=value` 配置行；主路径由 `memory_put()` 序列化（§5.2），
LLM 不用手写管道行。解析器**宽容**：字段顺序无关、接受常见别名
（`domain`/`site`、`kind`/`type`）、未知字段忽略、`ts` 可缺省。

### 二级：`playbooks/<domain>.md`（手动聚合，不是每次写）

同一 domain 命中 ≥2 次后，由 Agent 按模板把 `site-memory.lines` 里该域的
行聚合为 playbook（原子行保留，聚合只是视图）：

```markdown
# github.com

hits: 5 · updated: 2026-08-24

## 入口 / 登录
右上头像 → Settings

## 导航
Settings → Billing（导出入口）

## 已验证选择器
- `a[href="/billing"]` link

## 坑
- SSO 第二次点击被重定向回首页 → 等 3s 再点
- 登录弹窗是 iframe，AX 树拿不到 → 先切 frame

## 验证方式
URL == /billing 且出现表格
```

### 三级：`runbooks/<name>.py`（跑通的代码，一键复跑）

任务里写了很多代码才成功的流程，沉淀为**代码本身**——脚本跑通过就是
验证过的经验。头部 header 模板：

```python
# runbook: x-timeline-scrape
# 用途: 抓取 x.com 时间线最新 OBU_MAX_POSTS 条
# 前置: 已登录; 需要一个前台 tab (避开后台节流)
# 验证: len(posts)==N 且第一条 ts 为最新 (对照判成败)
# 参数: OBU_MAX_POSTS 默认 15
# 来源: 2026-08-25 任务; 滚动方案失败后改抓网络请求

posts = ...  # 跑通的完整脚本
```

- **一键执行**：`obu < runbooks/<name>.py`（复用现有 heredoc 模式，
  不新增执行器）。
- **跑通才入库**：保存前脚本必须真实通过一次。
- **验证即 stale 自检**：重跑后对照 header「验证」行判成败，失败 = 腐烂，
  标记 stale 后修复或删除——过期不用猜，复跑时自己暴露。
- **参数化**：用环境变量（`OBU_MAX_POSTS`），禁止复制粘贴改脚本。

### 技术层：`techniques/<name>.md`（跨域可迁移的解法）

按**可命名模式**索引，不按域名。一个文件一个模式：

```markdown
# name: virtual-scroll-throttle → network-scraping

## 关键词
滚动, lazy, infinite scroll, 虚拟列表, 节流, throttle, IntersectionObserver, 加载不全

## 触发信号
- 滚动抓取反复失败 / 数据静默缺失
- DOM 节点数远小于可视内容量（虚拟列表）

## 成因
- 虚拟滚动只渲染可见项 + 缓冲
- 后台 tab 的 IntersectionObserver / rAF 被 Chrome 节流 → 滚动加载静默失败

## 解法
- 放弃 DOM 滚动，改抓网络请求（Network domain 里的 XHR/fetch JSON）

## 来源站点
- x.com 时间线抓取（2026-08-25，已验证）
```

写入规则：新建前先对 `techniques/` 里的标题与关键词行做一次全量比对查重；
已有同名模式只追加「来源站点」，不新建文件（技术模式数量有界）。
**关键词行是召回面**：必须列同义词（滚动/lazy/infinite scroll/节流/throttle/…），
检索命中文档靠它，不靠文件名——写入时列得全，召回时 LLM 才不用猜。

## 5. 捕获路径（写入）

### 5.1 内容纪律：只写「贵的知识」

成功流程 Agent 每次都能重新推导，**不写**。只写三类：

1. 踩过的坑（失败 + 解锁）
2. 意外结构（iframe 弹窗、隐藏入口、反爬节奏）
3. 少而精的正面套路（跨多次访问验证过才值得）

银行流水式记录是噪声，会稀释检索价值。

### 5.2 写入方式：一个 helper，不是命令

记忆是文件，但写入这一个动作交给 helper，理由与 OBU 的既有原则一致：
**「模型不手写 JSON/shell 转义」**（Python heredoc prelude 就是为了消灭
手写序列化而存在的）。因此只在 `cmd/open-browser-use/script_helpers.py`
加一个 ~15 行的 `memory_put(...)`：

```python
memory_put("x.com", kind="surprise",
           note="时间线虚拟滚动, DOM 只渲染可见项+缓冲",
           unlock="改抓网络请求", tech="virtual-scroll-throttle")
```

- Python 参数传递：无转义、无字段拼接错误；helper 校验 kind、归并子域、
  append 安全（并发/重试不丢不覆盖），返回写入行的引用。
- `kind` 词汇表不靠先验：`help(memory_put)` 运行期自省 docstring
  （`kind: fail|surprise|tip`），写前现场查，开卷考试。
- 只写 `~/.open-browser-use/memory/site-memory.lines`（尊重 `OBU_MEMORY_DIR`）；
  不实现任何命令层。
- 非 OBU heredoc 环境（直接用文件工具）按 §4.1 的行格式手动 append 同样合法
  ——格式就是为手写安全的；解析器宽容（§4.1），写偏了也能被读懂。

技术文件（techniques/）与 playbook 的创建/更新仍是 Agent 用文件工具手写，
不在 helper 范围内。

### 5.3 明确不做：机制级自动捕获

扩展层自动记录「验证过的 selector + 快照」效果确实好，但需要改
MV3 extension、加重放验证与失效淘汰机制，成本显著高于收益。
**本设计把它排除**；作为补偿，`kind=surprise/tip` 行可携带 `selector` 字段，
由 Agent 在写入时顺手带上验证过的 selector，复用前低成本重放验证。

### 5.4 技术经验的提出（跨域迁移）

当解锁动作**可以迁移到其他站点/任务**时（后台节流、虚拟滚动、iframe 弹窗、
反爬节奏、网络兜底……），把「关键词 + 触发信号 + 成因 + 解法」提炼为
`techniques/<name>.md`，站点原子行只留一句 + `tech` 链接。
一次沉淀，全域复用；下次碰到别的无限滚动站点时，靠触发信号命中。

### 5.5 代码沉淀：跑通才入库，一键复跑

任务里写了很多代码才成功时，沉淀形态是代码本身：

1. 脚本**真实跑通**后才算 runbook（保存前必须通过一次）。
2. 用文件工具把脚本存为 `runbooks/<name>.py`，头部附模板
   （用途/前置/验证/参数/来源，见 §4.3）。
3. 一键执行：`obu < runbooks/<name>.py`。
4. 参数化用环境变量，禁止复制粘贴改脚本。
5. 保存前查重 `runbooks/`；**一次性任务脚本**（完成后不再复用）不入库，
   只留站点行/技术。
6. 复跑验证失败 = runbook 腐烂：标记 stale 并修复或删除。

判定分流：站点特有且稳定 → runbook；通用解法 → technique（解法段可附
代码片段）；一次性任务史 → 站点行（map, not diary）。像「抓网络请求」这类
endpoint 漂移风险高的，以 technique 为主，runbook 只在多次验证稳定后落。

## 6. 检索与复用路径（读取，全部原生工具）

skill 强制「读前写后」秩序，检索有两个触发器，全部用 grep/find/read
完成（具体命令写法沉淀在 `references/memory.md`）：

1. **按域预取**：访问一个尚未在本次会话建立经验的 domain 前：
   - `grep "domain=<domain>|" site-memory.lines` → 该域原子行；
   - 有 `playbooks/<domain>.md` / `runbooks/` 命中一并读（含 `runbook=` 链接的脚本）；
   - `@tech` 链接的技术文件也读出（反向绑定，双保险）。
2. **失败信号（机械化触发，不依赖猜词）**：同一目标尝试 ≥2 次仍失败、
   或出现「静默失败」（页面正常但数据缺失、滚动/事件不触发）时，
   **无条件**先全量读技术层索引（`find techniques/ -name "*.md"` 后
   逐文件读标题 + 关键词 + 触发信号，预算 ≤50 行），再按域预取。
   LLM 不需要猜词——索引全量可见，靠读触发信号自匹配。
   典型翻盘点：滚动抓不到 → 改抓网络请求。
3. **复用 runbook**：命中时一键复跑并对照 header 的「验证」判成败：
   `obu < runbooks/<name>.py`；验证失败即标记 stale。

## 7. 防爆炸治理（硬规则，全部可机检）

1. 一个 domain = 一个 key；**永不**新增站点级 skill / 站点级目录类型。
2. 一级是唯一写入入口；playbook/technique 只能按模板聚合或提炼，
   **禁止**随手开新 md（最重要的防膨胀闸门）。
3. 升级有阈值：playbook ≥2 次同域命中才聚合；技术文件新建前必须查重，
   已有同名模式只追加来源站点。
4. stale 清理：>90 天未命中（无新行、无读取）的 playbook/技术文件
   标记 stale，Agent 定期归档删除 → 总量有界。判定 = 文件 mtime +
   最近读取记录，纯文件操作。
5. skill 数量永久锁定为 1；对已有 domain 只追加，不做断言式覆盖。
6. 技术层是唯一允许的「跨域」目录；任何新目录类型一律视为违规。
7. 技术索引必须有界：全量索引输出预算 ≤50 行。可选的独立脚本
   `scripts/obu-memory-check.sh`（~30 行）校验：行格式、kind 合法、
   无临时 ref（`@N`/`ref=`）、无密钥/私人链接、索引预算——"能机检的
   约束变成机械检查"，但不进 CLI 命令面。
8. runbook 只收「跑通的代码」：header 必填（用途/前置/验证/参数/来源），
   复跑验证失败标记 stale；一次性脚本禁止入库。

## 8. 文件契约（替代命令层）

- 存储根：默认 `~/.open-browser-use/memory/`；`OBU_MEMORY_DIR` 覆盖
  （团队共享时指向 `docs/wiki/sites/` 之类仓库目录即可双栖）。
- 根目录 `README.md`：规则自描述——格式、决策树、每类一个示例；
  任何做记忆相关操作的 agent 应先读它（知识投递链 §9）。
- 一级文件：`site-memory.lines`，行格式见 §4.1；字段内禁用 `|`。
- 读约定：§6 的三条路径 + memory.md 里的具体 grep 命令。
- 写约定：OBU heredoc 内用 `memory_put(...)`；其他环境手动 append 同格式。
- runbook：`runbooks/<name>.py` 是完整 heredoc 脚本，一键执行 `obu < 文件`
  （复用现有 heredoc 模式，不新增执行器）；简单线性流程仍可用 `obu run`，
  但不作为记忆制品。
- **不新增任何 `obu memory *` 命令**：读是 agent 原生能力，命令层是
  对"读文件 + 拼渲染逻辑"的重复抽象。

## 9. 知识投递链：LLM 怎么学会记录（Skill 集成）

设计文档的格式规则是给**人/开发者**看的；真正让 LLM 知道怎么记录的是
一条三层投递链，全部不改 skill 结构：

### 9.1 三层投递

1. **SKILL.md 短入口**（每次 OBU 任务都加载，~10 行）：只写触发条件+
   行为约定，不背格式细节——
   - 收尾时评估经验；要写时先读 memory 根 `README.md` 或
     `references/memory.md`。
   - 存取路径、grep 写法、`memory_put` 的用法都在文档里，不写进 skill。
2. **`references/memory.md`**（写前现读，开卷考）：完整格式、决策树、
   每类制品一个完整示例（原子行 / technique / playbook / runbook）。
   只在即将写经验时读，不占日常上下文。
3. **memory 根 `README.md`**（自描述兜底）：没加载 skill、或换了个
   没装 skill 的环境的 agent，一旦碰 memory 目录就会读到它——规则
   跟着数据走，不依赖入口点。

### 9.2 收尾时的决策树（写在 memory.md / README 里）

```text
1. 有「贵的知识」吗？（坑/意外结构/解锁/可迁移解法/跑通的代码）
   没有 → 不写（顺利流程不值得记录）。
   有   → 下一步。
2. 归哪层？
   - 站点特有事实        → 原子行 site-memory.lines
   - 可迁移解法          → techniques/<name>.md（关键词+触发信号+解法）
   - 重复稳定流程        → runbooks/<name>.py（跑通的代码 + header）
   - 同域已 ≥2 次命中    → 顺手把原子行聚合进 playbooks/<domain>.md
3. 怎么落？OBU heredoc：memory_put(...)；其他环境：按 §4.1 行格式手写。
   不确定格式 → 读 README / memory.md。
```

### 9.3 三道兜底（写错了也不坏）

- **宽容解析**：字段别名/乱序/缺省都能读，错记不致命。
- **机械校验**：`scripts/obu-memory-check.sh` 查出坏行、临时 ref、密钥。
- **README 自描述**：错误永远可以参照规则被修正回来。

### 9.4 SKILL.md 新增小节草案

`skills/open-browser-use/SKILL.md` 新增一节，并新增
`references/memory.md`（格式 + 决策树 + 示例）：

```markdown
## 经验沉淀 (site memory)
- 存储：~/.open-browser-use/memory/（OBU_MEMORY_DIR 可覆盖），纯文件。
  读用 grep/find/read，不需要任何 memory 命令。
- 访问未知 domain 前：读该域原子行 + playbook + @tech 链接
  （`grep "domain=<domain>|" site-memory.lines`，写法见 references/memory.md）。
- 同一目标尝试 ≥2 次仍失败、或页面正常但数据缺失/事件不触发（静默失败）时，
  **无条件**先全量读 techniques/ 索引（标题+关键词+触发信号），再按域读；
  不猜关键词，不硬试更多轮。
- 收尾时只写「贵的知识」：OBU heredoc 内用 `memory_put(...)`（坑/意外结构/
  解锁动作，一行一个事实）；解法可迁移就提炼成技术文件（`tech=`）。
  顺利流程不写；敏感信息不写。
- 高价值流程存为 runbook（跑通代码 + header），一键复跑即验证：
  `obu < runbooks/<name>.py`。
- 写经验前不确定格式时，先读 references/memory.md（或 memory 根 README.md）。
- 细节与反爆炸规则: references/memory.md
```

## 10. 与仓库既有体系的分工

| 体系 | 承载 | 边界 |
|---|---|---|
| `docs/histories/` | 代码变更记录 | 不改 |
| `docs/wiki/` | 长期主题知识（逆向、调研） | 不改；`OBU_MEMORY_DIR` 可指向 `docs/wiki/sites/` 做团队层 |
| `docs/references/` | 外部参考材料 | 不改 |
| site memory | 站点操作级经验数据 | 默认在用户目录，跨 repo 跟随用户 |

技能面不变、既有文档体系不变，新增的只是一个数据层 + 一个 ~15 行 helper。

## 11. 明确排除（成本 > 收益，不做）

- 会话日志自动蒸馏：提取出的常是流水账而非经验，依赖日志结构化。
- 语义检索型记忆（会话即记忆）：去噪难、无法保证「当时成没成」。
- 按动作类（配方库）键控：早期评估与 domain 键控冲突；后吸收为**技术层
  第二维度**（§4.4 / §5.4），不再作为独立方案排除。
- 扩展层机制级捕获：需改 MV3 extension + 重放验证，成本高（见 5.3）。
- **任何 `obu memory *` 命令层**：读是 agent 原生能力，命令是重复抽象。
- 一次性任务脚本入库：完成即弃的代码不是资产，只留站点行/技术。

以上若未来要做，都应作为独立设计重新评审，**不得**在未评审时混入本机制。

## 12. 参考：ego-browser learnings（吸收三处）

对照 ego-lite `skills/ego-browser/learnings/<site>/`（manifest.json + notes/ +
tools/ + browser-tools/，运行时 `site.learnContext(url)` 按域匹配、`site.runTool`
执行带 schema 的 JS 工具），吸收三点，不照搬：

1. **「map, not diary」纪律**：只记站点的形状（结构/入口/坑），不记任务流水账。
   等价于本设计的 trap-map 内容纪律，规范里统一用这个表述。
2. **升级层可机检**：`scripts/obu-memory-check.sh` 校验 playbook/technique——
   域名合法、无临时 ref（`@N`/`ref=`）、无密钥/私人链接、行格式、索引预算。
   不要求行为测试，但把「能机检的约束」从口头规范变成机械检查
   （独立脚本，不进命令面）。
3. **不内嵌进 skill**：ego 把 pack 放进 skill 包随发布分发，一是门槛高到
   只有 2 个站点进入，二是 skill 包体积随站点数膨胀。本设计保持经验是
   skill 外的数据层，`OBU_MEMORY_DIR` 指向仓库目录即为团队共享版的分发。

不吸收的部分：manifest + 工具 + 行为测试的包结构（正好对应本设计
「一行捕获 + 手动聚合升级」的升级层，见 §5 / §7）。

## 13. 一期实现清单（按本设计一次落地，含上节吸收项）

1. `skills/open-browser-use/references/memory.md` — 格式、路径、grep 写法、
   决策树、每类一个完整示例、`memory_put` 用法。
2. `SKILL.md` 新增「经验沉淀」一节（见 §9）。
3. `cmd/open-browser-use/script_helpers.py` 新增 `memory_put(...)`（~15 行）。
4. 可选：`scripts/obu-memory-check.sh`（~30 行，机械校验含 runbook header 必填项）。
5. 记一份 `docs/histories/` history。