## [2026-08-28 11:45] | Task: 评估并全盘抛弃「高效 DOM 操作」方案（分支已删）

### 🤖 Execution Context

- **Agent ID**: pi（open-browser-use 仓库协作代理）
- **Base Model**: Claude（当前模型）
- **Runtime**: pi coding agent

### 📥 User Query

> 评估 `~/.pi/chats/obu-efficient-dom-plan.md`（v1）。若可行且收益大则在新分支实现；
> 实测后结论不支持，最终指示：删掉该分支，全盘抛弃该方案。

### 📋 决策结果

**全盘抛弃。分支 `feat/obu-efficient-dom`（4 个 commit：2fa6936、e2bdc0f、03313e0、af80cb7）
已删除，main 回退到 `b524177`（feat(cli): evaluate() 与 browser-harness js() 语义对齐）。**
本 history 仅记录决策闭环，避免未来 Agent 重复评估同一方案；方案代码未保留。

### 🛠 走过的完整闭环（为什么抛弃）

1. **实现（约一天）**：按方案 v1 完成 P0+P1+P2——
   扩展端 `snapshot` handler（AX 紧凑树 + `@eN` ref + cursor_scan）；
   Python prelude 14 个 helper（snapshot/click/fill/check/uncheck/scroll/
   get_text/get_value/is_visible/is_checked/diff_snapshot/locate/
   wait_for_selector/wait_for_network_idle）+ 错误分层 + 遮挡拦截 + 失效重解析；
   context-footprint 评测 + SKILL「高效工作流」+ 全套单测（扩展 8 文件、
   Python 77 用例）+ 真机（真实 Chrome）验证。
2. **实测证伪核心叙事（两次真机）**：
   - 数据抓取任务（x.com 15 帖 ×2）：snapshot/@ref 对抓取本无价值（正文不在
     AX 交互树里），108s 的瓶颈是 agent 编排（5 个脚本往返），非 CDP 调用——
     单脚本模板可压到 20s，但这与高效层无关。
   - 交互型 A/B（受控表单 4 步流程，路径 H=高效层 vs E=evaluate 手搓）：
     时间打平（3.0s/3.0s，H 反而 3.31 vs 2.70），上下文输出 H 反而更大
     （探路 403B/11 行 vs 193B/1 行；总 stdout 569B vs 350B）。两条路径全部
     正确完成。（A/B 脚本与 fixture 已 cleanup，未入库。）
   - 结论：agent 会把观察压到最小（一条 evaluate 数字/数组），紧凑树只在需要
     "全景"时才赢；且真实基线是 evaluate 而非原始 AX dump，footprint 证明
     打错了靶子。**"省 token/时间"主张零支持，复杂度账单（+2.5k 行）真实。**
3. **回滚**：跟随用户指示删除分支。git 历史未 stash，可随时按 commit 检索。

### 🧠 沉淀的经验（避免重蹈）

- 效率类方案必须先跑"真实 agent 基线"的 A/B 再写代码；基线是"agent 实际会写的
  evaluate"，不是"现有接口的最差用法"。
- 抓取 vs 交互是两个不同战场：抓取=结构化 evaluate+滚动收集；交互=定位+输入+
  验证。宣称收益时要对着真实任务类型。
- 真机 A/B 中仍抢救出两个与方案无关的独立正确修复，随分支一并删除：
  `wait_for_selector` 此前复用单元素唯一性解析导致多匹配误报「ambiguous」的
  语义问题、`scroll()` 原语、`evaluate()` IIFE 提示、SKILL 单脚本模板。若未来
  需要，可从 `af80cb7`/`03313e0` 按需 cherry-pick（不依赖被抛弃的 snapshot/
  ref 引擎）。

### 📁 Files Modified

- 删除分支 `feat/obu-efficient-dom`；main 无代码变更（`git checkout main` +
  `git branch -D`）；本 history 文件为唯一新增。