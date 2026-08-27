## [2026-08-27 17:15] | Task: `obu pyscript <file>` — 任务脚本文件执行 + 准确 traceback

### 🤖 Execution Context

- **Agent ID**: pi (coding agent)
- **Base Model**: claude (当前会话模型)
- **Runtime**: pi harness, macOS

### 📥 User Query

> 延续任务工作区方案(上轮已交付 `tasks/<domain>/<slug>/` 沉淀 + `obu tasksizes`),
> 本轮"继续"落地配套短板:脚本已落盘,但 `obu < file` 仍走 stdin 管道,
> traceback 显示 `<stdin>` 且行号含 604 行 prelude 偏移,方向/语法错误定位成本高。

### 🛠 Changes Overview

**Scope:** `cmd/open-browser-use`(新命令 + runner + 测试)、`skills/open-browser-use`、
`docs/ARCHITECTURE.md`

**Key Actions:**

- **`obu pyscript <file.py>` 新命令**(`script.go` + `script_runner.py`):以真实
  文件路径执行任务脚本。Go 复用 `resolveScriptSocket`,把 go:embed 的 prelude
  (`script_helpers.py`)与 runner 写入每次调用独立的临时目录,spawn
  `python3 -u <runner> <file>`;runner 把 prelude 作为模块 `sys.path` 导入、
  `vars()` 全量注入全局(排除 dunder,保留 `_SESSION_ID`/`_help` 等单下划线名,
  与 heredoc 拼接模式表面一致),再 `compile(source, file, 'exec')` 执行——
  traceback 携带真实文件路径与文件内行号。
- **实机对比验证**:同名脚本 `pyscript` 报
  `File ".../05_parse.py", line 5, in main`,heredoc 报
  `File "<stdin>", line 610`(609/610 = prelude 604 行 + 文件 5/6 行)。
- **env 统一**:抽出 `scriptSubprocessEnv`,heredoc 与 pyscript 都把
  `--session-id` flag 显式注入子进程 env(非空才注入,flag 优先于继承环境),
  修复 `socketOptions.sessionID` 之前到不了 Python 侧的语义缺口。
- **测试**(`script_test.go` 新增 7 个用例):纯 python 执行、退出码透传、
  语法错误带真实路径且不含 `<stdin>`、运行时错误行号为文件本地行号、
  prelude 表面与 heredoc 一致(`_SESSION_ID`/`cdp.__doc__`)、helper 错误延迟
  契约、文件不存在报错。
- **skill/docs 同步**:SKILL.md Task workspace 与 references/memory.md 的
  执行方式从 `obu < file` 改为 `obu pyscript <file>`;ARCHITECTURE.md 命令
  清单加 `pyscript`,heredoc 段补实现说明(A 段)。

### 🧠 Design Intent (Why)

任务工作区把脚本变成了一等公民,但 heredoc 执行路径把它们压回匿名 stdin:
traceback 行号错位 604 行,agent 无法把报错行映射回编辑器里看到的文件。
`pyscript` 用 `compile(source, filename, 'exec')` 让 Python 把文件路径烧进
code object——零行号重映射 hack,纯语言语义。命名沿用单词约定,不与既有
`obu run`(line-oriented action plan)冲突;heredoc 主路径保持不动(兼容),
`obu < file` 仍可用。临时目录每调用写 2 个小文件、用完清理,无持久污染。

### 📁 Files Modified

- `cmd/open-browser-use/script.go`(runExecFile + newExecCommand +
  scriptSubprocessEnv,heredoc 路径同步用 env 助手)
- `cmd/open-browser-use/script_runner.py`(新,go:embed)
- `cmd/open-browser-use/script_test.go`(新增 7 个 exec 用例)
- `cmd/open-browser-use/main.go`(注册 newExecCommand)
- `skills/open-browser-use/SKILL.md`、`references/memory.md`(执行方式改为 exec)
- `docs/ARCHITECTURE.md`(命令清单 + exec 实现说明)

### 🔁 后续修订(2026-08-27)

- 命令名 `pyscript` 改为 **`exec`**(用户偏好更短的单词命名):
  `newPyscriptCommand`→`newExecCommand`、`runPyscriptFile`→`runExecFile`、
  `OBU_PYSCRIPT_PRELUDE_DIR`→`OBU_EXEC_PRELUDE_DIR`、临时目录前缀
  `obu-pyscript-`→`obu-exec-`,skill/docs/错误文案同步,
  测试沿用并跑通;归档 history 保留原名以便追溯。
- Skill 教学面不再包含 heredoc(`obu <<'PY'` 示例全部移除;references 的
  "heredoc"措辞统一改为"script"/"inside a script")。Browser work 主路径
  改为:脚本写入任务工作区 + `obu exec <file>` 执行(文件即真相,
  traceback 行号对齐文件);Process model、runbook rerun 命令
  (`obu exec runbooks/x.py`)同步;实现层 heredoc 支持保留在 CLI 中,
  只是不再出现在 skill 教学面。
- **`help("name")` 字符串形式可用**(真机测试反馈):pydoc 对字符串参数
  只做模块 import 解析(`locate`),字符串形式在脚本模式
  (heredoc 与 exec 皆然)原本一律失败——REPL 习惯的 agent 会踩到。
  prelude 末尾加容错 `help` 包装:字符串先在脚本全局解析(命中即输
  出 docstring),未命中/对象/无参回退内建行为。SKILL.md 注明两种形式
  等价;新增 prelude 单测(字符串/对象/未知名回退)与 heredoc、exec
  集成测试。
- **`obu api-sheet` + 探索压缩**(fresh run 省 token):一次命令输出全部
  helper 的一行速查(`inspect.signature` 运行时签名 + docstring 首行,
  不随 prelude 演进过时),替代 fresh agent "为学 API 写脚本轮番
  `_help()`/`help()`" 的 2-3 轮探索。prelude 新增 `_api_sheet()`
  (helper 名单重构为模块级 `_HELPER_NAMES` 共同供 `_help` 使用;
  SIGPIPE 设回默认值,管道被 head 截断时不打 BrokenPipeError)。
  Go 新增 `obu api-sheet` 命令(spawn python3 跑纯 prelude,不解析
  socket、不碰浏览器)。skill 教学改两处:Setup 段加第 3 步
  (per-task `obu api-sheet` + 只用 `help(name)` 查要用的那个)、
  Learn the API 段改为 api-sheet 优先 + 红线("不要为学 API 单独
  跑脚本,把 `print(help(x))` 折进正在写的脚本")。按用户要求
  **skill 不放任何脚本模板内容**。
- **Setup 三连**(skill 教学面):首轮一条命令
  `obu profiles --connected && obu ping && obu api-sheet` 完成环境巡检
  + API 速查,替代三步独立轮;profiles 放最前(它是 browser/profile
  决策输入,输出决定后续 env)。保留硬语义:多 target 必须问用户、
  不得静默选 active window、未连接要重试。
- **`obu taskinit` 任务启动仪式一条龙**(替代三连):一次调用完成
  选 target(单 target 自动采用;多 target 报错并列出带可复制 rerun
  命令,硬规则不静默选)→ 建 `tasks/<domain>/<slug>/` → 写 `.obu.env`
  (OBU_SESSION_ID=obu-<slug>/browser/profile;已存在则保留并沿用其
  session id,同一任务目录恒为同一 session)→ ping 验证 → 输出
  api-sheet。输入防碰撞:domain 归一化(去 www. 小写,与 site-memory
  一致)、slug 拒绝 `/`/`\`/`.`/`..`/超长/审计保留名(`__pycache__`/
  `.DS_Store`),防破坏二层布局、路径逃逸与 tasksizes 统计盲区。
  `obu exec <file>` 只读脚本**同目录** `.obu.env`(不向上查找,任务
  强相关锚定任务目录;父级同名文件不会泄漏);`--env-file` 显式覆盖;
  优先级 flag > env-file > 继承 env(flag 显式时命令层从文件 env 剔除
  对应键，保证 prelude 读到 flag 值)。新增 `mergeEnvVars`/`parseEnvFile`/
  `loadTaskEnv`/`resolveTaskTarget`/`writeTaskEnvFile` 及 9 个单测;
  skill Setup/Start/Task workspace 同步(taskinit 为推荐路径,手动 env
  与 flags 仍可用)。
- **taskinit 时间戳实例**(防同名碰撞):每次 taskinit 创建
  `<slug>-<YYYYMMDD-HHMMSS>` 独立实例目录,session id 同步为
  `obu-<实例>`,重复运行同一 `<domain>/<slug>` 不再碰撞(并发 agent、
  多次实验各自隔离)。续跑旧任务直接 `obu exec` 旧目录脚本(.obu.env
  自动加载),不需要重新 taskinit;writeTaskEnvFile 的 kept 路径退化为
  同秒碰撞兜底。真机验证:同名两次 taskinit 产出
  `timeline-posts-20260827-233317`/`-233318` 两实例,排序即时间序。