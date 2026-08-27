# 任务脚本按 domain/task-slug 沉淀 + `obu tasksizes` 体积审计

## 目标

让每个 OBU 任务的脚本按 `<domain>/<task-slug>/` 沉淀到任务工作区
(`$OBU_TASK_DIR`,默认 `~/.open-browser-use/tasks/`),失败尝试留盘、
可 diff、可复盘、稳定流程可升级为 runbook;新增 `obu tasksizes` 命令
按域名分组审计任务目录体积与膨胀项,支持 `--older-than --dry-run`
列出可清理项(本期只观测,不删除)。

## 范围

- 包含:
  - `cmd/open-browser-use/tasksizes.go`:`obu tasksizes` 子命令(单词命名,
    无连字符),遍历任务根按 domain 分组汇总(任务数/文件数/大小),
    域内 largest 明细;人类可读表格 + `--json`;`--path` 覆盖任务根
    (默认 `$OBU_TASK_DIR` → `~/.open-browser-use/tasks`);`--sort size|name`
    (默认 size 降序,膨胀优先);`--older-than <days> --dry-run` 列出
    idle 超期候选(只列不删,本期强制只读)。
  - 扫描排除: `__pycache__/`、`*.pyc`、`.DS_Store`。
  - `docs/exec-plans/active/` 归档为 `completed/` + `docs/histories/`
    记录本次变更。
  - 测试 `cmd/open-browser-use/tasksizes_test.go`:临时目录树上验证
    分组/汇总/排除/`--json`/dry-run 只读、按 size 排序。
  - skill 最小增量(SKILL.md Start a task 一条硬规则:任务工作区目录 +
    `obu < file` 执行),不引入任务目录细节与统计命令。
  - docs 同步:ARCHITECTURE.md 命令清单加 `tasksizes` + 任务工作区一段;
    skill `references/memory.md` 加任务工作区并行根与升级 runbook 的关系
    (几行,供 agent wrap-up 时对齐)。
- 不包含:
  - `obu run <file>`(prelude 模块化 / traceback 行号重映射):后续迭代,
    本期脚本执行沿用既有 `obu < file` 管道能力。
  - 自动清理 / `--prune`:本期绝不删除,只 dry-run 列表。
  - heredoc 工作流、Chrome 扩展 / native host 协议(不动)。

## 背景

- 相关文档:skill `SKILL.md`(Start a task / End a task)、
  skill `references/memory.md`(site-memory 约定,domain 归一化规则)、
  `docs/ARCHITECTURE.md`(CLI 命令清单)。
- 相关代码路径:`cmd/open-browser-use/main.go`(root 命令注册、
  `newVersionCommand` 等命令模式)、`cmd/open-browser-use/script.go`
  (`obu < file` 管道执行,不改)。
- 已知约束:
  - domain 归一化与 site-memory 一致(去 `www.`、小写)。
  - task-slug 与 `OBU_SESSION_ID` 的 slug 对齐(skill 已是
    `obu-<task-slug>` 格式)。
  - 命令命名用户拍板:单个词 `tasksizes`,不用连字符。

## 风险

- 风险:任务目录随时间膨胀,自动清理误删(脚本可能被 runbook 引用、
  任务可能跨月继续)。
  缓解:本期只观测 + dry-run 列表,不提供删除能力;stale 规则对齐
  memory 的 90 天惯例,将来若加 `--prune` 必须先有 runbook 引用白名单。
- 风险:`--older-than` 的 idle 语义(目录 mtime vs 文件 mtime)造成误判。
  缓解:任务 idle 取"目录内最新文件 mtime 与目录 mtime 的最大值",
  文档写明口径。

## 里程碑

1. 调研与方案收敛(已通过 design deck 审核:dirlayout=并行根
   `~/.open-browser-use/tasks/`;cmd=单词 `tasksizes` 表格 + `--json`;
   hygiene=观测 + dry-run 列表;skill 增量尽可能小,不必要 LLM 知道的
   不写)。
2. 实现 `obu tasksizes`(扫描 + 汇总 + 输出 + flags)。
3. 测试(分组/排除/json/dry-run/sort)。
4. skill 一行 + docs 同步。
5. 验证、归档 plan、记 history。

## 验证方式

- 命令:`go build ./... && go test ./cmd/open-browser-use -run Tasksizes -v`
- 手工:临时目录树造 3 个 domain / 6 个任务的样例,运行
  `obu tasksizes --path <tmp>` 与 `--json` 核对分组、largest、total;
  `--older-than 0 --dry-run` 应列出全部任务且不产生任何删除。
- 观测检查:输出含 root 路径、域汇总、largest per domain;空/不存在
  的根输出提示且退出码 0。

## 进度记录

- [x] 确认范围和约束(design deck 审核已确认)。
- [x] 实现 `obu tasksizes` 扫描与输出。
- [x] 完成单测并全绿。
- [x] skill / docs 同步。
- [x] 归档 plan 到 completed，记 history。

## 决策记录

- 2026-08-27:任务工作区用并行根 `~/.open-browser-use/tasks/`
  (`$OBU_TASK_DIR` 可覆盖),与 `memory/` 分层,不影响 memory 的
  hygiene 规则。
- 2026-08-27:命令名 `tasksizes`(单词),输出人类可读表格 + `--json`。
- 2026-08-27:本期只观测(+ `--dry-run` 列表),不实现自动清理;
  stale 口径 90 天起列候选。
- 2026-08-27:skill 增量最小化——只加一条硬规则,任务目录结构 /
  统计命令细节只落 docs,不写进 LLM 必读面。