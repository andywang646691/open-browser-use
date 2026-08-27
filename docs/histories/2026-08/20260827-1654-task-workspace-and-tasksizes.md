## [2026-08-27 16:54] | Task: 任务脚本按 domain/task-slug 沉淀 + `obu tasksizes` 体积审计

### 🤖 Execution Context

- **Agent ID**: pi (coding agent)
- **Base Model**: claude (当前会话模型)
- **Runtime**: pi harness, macOS

### 📥 User Query

> Agent 跑 OBU 时经常中途出错(语法/字段/方向错误)导致重写,浪费执行速度与
> token。经多轮方案讨论后拍板两项修改:1) 每个任务按 `<domain>/<task-slug>`
> 目录沉淀脚本;2) 新命令按域名分组审计这些沉淀文件的大小。方案经 design deck
> 审核,反馈三处:skill 增量尽量小(不必要 LLM 知道的不写)、命令用单个词不用
> 连字符、防膨胀只观测 + dry-run 列表不清除。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use`(新子命令)、`skills/open-browser-use`(
SKILL.md 一行 + references/memory.md 一小节)、`docs/ARCHITECTURE.md`、
`docs/exec-plans/`(active → completed)

**Key Actions:**

- **`obu tasksizes` 新命令**(`tasksizes.go`):遍历任务根(默认
  `~/.open-browser-use/tasks/`,`$OBU_TASK_DIR` 或 `--path` 覆盖),按域名
  分组汇总(任务数/文件数/大小)+ 域内 largest 明细;人类可读表格 +
  `--json`;`--sort size|name`(默认 size 降序,膨胀优先);
  `--older-than <days> --dry-run` 列出 idle 超期候选,**本期只读不删**
  (无 `--prune`,强制 dry-run 才能带 `--older-than`)。
- **扫描语义**:root 一层 = domain,二层 = task-slug;domain 下散文件计入
  域汇总但不计任务数;排除 `__pycache__/`、`*.pyc`、`.DS_Store`;
  **任务的 idle 只以目录内最新文件 mtime 为准**——目录 mtime 反映
  创建/改名而非活动,若不排除会把 rename 后的旧任务误判为活跃
  (真机 demo 中 200 天前任务被新 mtime 目录拖回 idle=0,已修正);
  空任务目录退化为自身 mtime。
- **任务工作区约定**:脚本按 `tasks/<domain>/<slug>/` 沉淀,domain 归一化
  与 site-memory 一致(去 `www.` 小写),slug 与 `OBU_SESSION_ID` 对齐;
  执行用既有 `obu < file` 管道(零 CLI 改动),失败尝试留盘可 diff 可复盘,
  稳定流程 wrap-up 时升级为 memory/runbooks。
- **skill 最小增量**:SKILL.md 只加 "Task workspace" 一条硬规则(目录 +
  `obu < file`),不引入目录结构/统计命令细节;references/memory.md 加
  并行根说明与 runbook 升级路径。
- **测试**(`tasksizes_test.go`):临时目录树上验证分组/汇总/排除规则/
  human+json 输出结构/`--sort name`/stale 只列不删/文件老化不计目录
  mtime 的 idle 语义/humanBytes。

### 🧠 Design Intent (Why)

方向错误的根因之一是"脚本不留档、错误不可复盘"。把脚本按
domain/task-slug 落盘后:失败尝试有 diff 账本、稳定流程能升级 runbook、
体积膨胀可审计可观测。命令命名刻意单词化(`tasksizes`)避免新词汇教学;
职责边界刻意只观测不删除——误删成本大于磁盘成本,清理留到有 runbook
引用白名单之后的迭代。skill 遵循"固定 skill 增长数据"原则:工作流心智
模型一条规则进 SKILL.md,实现细节进 references/文档。

### 📁 Files Modified

- `cmd/open-browser-use/tasksizes.go`(新)
- `cmd/open-browser-use/tasksizes_test.go`(新)
- `cmd/open-browser-use/main.go`(注册 `newTasksizesCommand`)
- `skills/open-browser-use/SKILL.md`(Task workspace 一节)
- `skills/open-browser-use/references/memory.md`(并行根说明)
- `docs/ARCHITECTURE.md`(命令清单 + 任务工作区一段)
- `docs/exec-plans/active/2026-08-27-task-workspace-and-tasksizes.md` → completed