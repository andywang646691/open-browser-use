# Heredoc 脚本模式重写：真 Python 执行器

## 目标

把 `obu <<'PY'` heredoc 从自写 Python 子集解释器改为真 Python 执行器
（Go spawn `python3` + 内嵌 prelude），消灭 LLM 语法幻觉；API 契约由
prelude 内每个 helper 的工程化 docstring 承载，LLM 用 `help(fn)`/`dir()`
现场自省；skill 只教工作流心智模型，不再依赖 API 清单。

## 范围

- 包含：
  - `cmd/open-browser-use/script.go` 重写：管道 stdin → spawn `python3 -u -`，
    stdin 喂 `go:embed` 的 prelude + 用户脚本；stdout/stderr 原样透传；
    python 非零退出码 → CLI 非零退出。
  - 新增 `cmd/open-browser-use/script_helpers.py`（go:embed 源文件，也是
    Python 单测的直接测试对象）：标准库实现 Browser Use JSON-RPC frame
    编解码（4 字节 native-endian length + JSON，对齐 `internal/wire`），
    helper：`user_tabs`/`tabs`/`claim_tab`/`open_tab`/`navigate`/`history`/
    `name_session`/`cdp`/`evaluate`/`finalize_tabs`，每个带 docstring
    （签名 + 返回结构 + 例子）。
  - socket 解析保留在 Go 侧：复用现有 `--socket`/`--socket-dir`/env/
    active registry 逻辑，解析出的最终 socket 路径经环境变量传给 prelude。
  - `cmd/open-browser-use/script_test.go` 重写：lexer/parser/eval 测试退役；
    保留 session env、TTY/空 stdin、管道模式等行为测试；新增 prelude
    Python 单测（临时 socket 假 server）和 spawn 集成冒烟。
  - skill 与 `docs/ARCHITECTURE.md` 的 heredoc 描述同步更新。
- 不包含：
  - `obu run` action plan 入口（不动）。
  - Chrome 扩展 / native host / socket 协议（不动）。
  - daemon 常驻模式：跨 heredoc 状态保持维持现状（浏览器 tab 状态本就在
    扩展 session 里；Python 变量单进程内有效）。
  - Windows python3 打包分发；python3 缺失时给友好报错即可。

## 背景

- 相关文档：`docs/ARCHITECTURE.md`（heredoc 段落）、
  `skills/open-browser-use/SKILL.md`、
  `docs/histories/2026-08/20260824-1149-obu-skill-llm-first-redesign.md`。
- 参考实现：`~/workplace/harness/browser-harness`（`exec(code, globals())`
  真 Python + docstring 承载 API 契约 + skill 只教工作流）。
- 相关代码路径：`cmd/open-browser-use/script.go`（1338 行 lexer/parser）、
  `script_test.go`（551 行）、`main.go:84-92`（调用点）、
  `internal/wire/frame.go`（frame 协议）。
- 已知约束：CLI 无参数 + 管道 stdin = 脚本模式，TTY = startup status；
  `OBU_SESSION_ID`/`OBU_BROWSER`/`OBU_PROFILE` 环境变量兜底语义保留；
  macOS/Linux 自带 `python3`。

## 风险

- 风险：python3 缺失（Windows 常见）。
  - 缓解：`exec.LookPath` 检测，报错信息明确写"heredoc 脚本模式需要
    python3"；CLI 其余功能不受影响。
- 风险：Python 端 frame 编解码与 Go wire 不一致。
  - 缓解：prelude 单测用临时 socket 假 server 覆盖编解码 + helper 调用
    序列；`internal/wire/frame.go` 是 20 行的简单协议。
- 风险：输出格式从 JSON lines 变为原生 Python 文本，破坏旧消费者。
  - 缓解：这是设计意图（LLM 消费原生 stdout）；skill 同步改写示例。
- 风险：每次 heredoc spawn python3 有启动开销（约 50ms）。
  - 缓解：可接受；不进热路径。

## 里程碑

1. prelude Python 模块 + Python 单测（临时 socket 假 server）。
2. `script.go` 重写（spawn + embed + env 注入）+ Go 测试重写。
3. skill / ARCHITECTURE 更新 + 全量验证 + history。

## 验证方式

- 命令：
  - `go test ./cmd/...`（重写后全绿）。
  - `make local-install` 后集成冒烟：
    - `obu <<'PY'` `print(1+1)` → `2`（真 Python 生效）
    - `obu <<'PY'` `print([i for i in range(3)])` → 列表推导可用
    - `obu <<'PY'` `help(open_tab)` → docstring 可自省
    - `obu <<'PY'` `cdp = 1` 之类错误 → traceback + 非零退出
    - 管道空输入 → 仍走 startup status
- 手工检查：把上一个任务的"LLM 误会四连"（if/比较/f-string/推导）全部
  跑通；浏览器链路由用户实测。
- 观测检查：无（纯本地 CLI 行为）。

## 进度记录

- [x] 确认范围和约束。
- [x] prelude + 单测完成（`script_helpers_test.py` 11 用例全绿）。
- [x] script.go 重写 + Go 测试完成（`go test ./cmd/...` 全绿）。
- [x] skill / 文档 / history 完成。
- [x] 全量验证通过（集成冒烟：真浏览器链路 user_tabs/open_tab/evaluate/
  cdp/finalize_tabs 全通，列表推导/if 分支/traceback 透传/退出码透传验证）。
- [x] （追加，2026-08-24/25）实测发现并修复 prelude 响应/通知错位：
  `onCDPEvent`/`heartbeat` 通知帧被误读为响应，`cdp(...)` 返回 None →
  `TypeError`。修复为请求 id 自增唯一 + 循环按 id 匹配响应、跳过通知；
  `script_helpers_test.py` 补 4 个用例。详见
  `docs/histories/2026-08/20260825-0003-prelude-response-id-correlation.md`。

## 决策记录

- 2026-08-24：heredoc 改为真 Python 执行器，废弃自写子集 lexer/parser。
  理由：LLM 对真 Python + CDP/Playwright 词汇有先验知识，子集语言制造
  系统性语法幻觉（实测 if/比较/f-string/推导全报错且报错不指方向）。
- 2026-08-24：API 契约放 prelude docstring，不放 skill。
  理由：参考 browser-harness 的证明——契约随代码走天然不过期，Python
  `help(fn)` 是 LLM 零成本自省路径；skill 只教工作流 + 一句"不确定就
  help(fn)"。
- 2026-08-24：不引入 daemon。obu 的跨调用 tab 状态已由扩展 session
  承载，browser-harness 的 daemon 是为了持有 CDP 连接，obu 无此需求。
- 2026-08-25（追加）：prelude 请求/响应改为 id 关联 + 通知帧跳过。
  理由：Python SDK 的 `request()` 早已具备同一保护，prelude 漏掉后，
  x.com 式导航事件风暴会让 `cdp(...)["nodes"]` 读到 `onCDPEvent`
  通知帧（无 result）返回 None；修复不触碰 wire 协议与 relay。