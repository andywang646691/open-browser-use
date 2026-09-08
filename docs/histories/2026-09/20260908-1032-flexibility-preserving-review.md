## [2026-09-08 10:32] | Task: 复审并修复 OBU 任务运行时边界

### Execution Context

- **Agent ID**: OpenAI coding assistant
- **Base Model**: 当前 API 未提供可靠的具体型号标识
- **Runtime**: pi coding agent

### 用户诉求

> 重新审视项目，在不丢失灵活性的前提下优化；必要时参考包含 OBU 使用的 pi session。
> 用户确认使用独立 worktree，并允许只读最近 30 天相关 session 的调用模式。

### Changes Overview

**Scope:** Go CLI taskinit/exec、内嵌 Python prelude、测试/CI、文档与 skill。

- 通过仓库历史与脱敏使用模式确定方向，避免重做已被 A/B 否定的 snapshot/ref 引擎。
- 修复 `wait_for_event` 丢弃非匹配事件的问题；仅消费匹配项，保持 FIFO 有界尾队列，predicate 异常保留事件；所有路径恢复原 socket timeout。
- request/wait 共享半帧缓存，读超时后继续同一帧；绝对 deadline 不被持续通知续期；匹配响应按 id，null/0 响应不误入 notification 队列；帧上限对齐 Go 64 MiB。
- 首次连接失败关闭候选 socket；sendall 失败后停止复用连接，绝不自动重放有潜在副作用的动作。事件错误 URL 去除凭据、query、fragment，不宣称全面脱敏。
- `exec <file> -- <args...>` 支持脚本参数，用标准库 runpy 提供正确 `__file__`/`__main__`，可 import 同目录模块，保持 cwd/traceback/退出码与 helper 注入。
- 保留小数秒 timeout；清除继承的过期 socket error；api-sheet 兼容缺少 SIGPIPE 的平台。
- taskinit 同时匹配 browser/profile（含继承环境默认值），保存完整 browser instance target；无法映射 profile 时锚定 extension instance id，多匹配仍询问用户，完整显式离线配置仍可建工作区。
- domain/slug 校验路径、控制字符与审计保留名，拒绝 domain symlink；以原子分配的带唯一后缀目录和含 domain 的 session id 避免同秒并发/跨域碰撞；旧任务不迁移。
- 将此前未被 make ci 运行的 Python helper 测试纳入 CI，新增 16 项 transport 回归与 Go 文件执行/并发/selector 测试。
- RELIABILITY 从占位说明补成运行时契约，修正架构/安全里 SDK 发布、默认窗口模式和后台唤醒范围的旧说法；skill 不再重复请求 taskinit 已打印的 api-sheet。

### Design Intent

修复基础设施的确定性，而不是用更多 prompt、站点专用逻辑或高级封装掩盖问题。
原始 JSON-RPC `call`、`cdp()`、完整 Python、run/heredoc、现有内部 SDK/MCP 源码均保留。
没有新依赖，不改 extension 的 Chrome 行为。

### 验证

- 基线与改后 `make ci`：通过。
- Python helper/transport：60 项通过，含 strict ResourceWarning 检查。
- `go test -race ./...`、`pnpm typecheck`：通过。
- selector / 并发任务用例重复 20 次：通过。
- `git diff --check`：通过。
- Go / Python / Chrome fake 不是本轮真实 Chrome 验证；未重载扩展、替换全局二进制、操作用户网页或发布版本。

### Files Modified

- `cmd/open-browser-use/script.go`、`script_runner.py`、`script_helpers.py`
- `cmd/open-browser-use/taskinit.go`
- `cmd/open-browser-use/script_test.go`、`script_helpers_test.py`、`script_transport_test.py`
- `cmd/open-browser-use/taskinit_test.go`、`taskinit_isolation_test.go`、`tasksizes_test.go`
- `scripts/ci.sh`
- `docs/RELIABILITY.md`、`ARCHITECTURE.md`、`SECURITY.md`、`CICD.md`、`QUALITY_SCORE.md`
- `docs/exec-plans/completed/2026-09-08-flexibility-preserving-review.md`
- `docs/exec-plans/tech-debt-tracker.md`、`docs/releases/feature-release-notes.md`
- `skills/open-browser-use/SKILL.md`、`references/troubleshooting.md`

### 用户复审后的命名简化

用户确认唯一后缀已负责实例区分，要求去掉冗余目录时间戳。新目录为
`tasks/<domain>/<slug>-<unique>/`，session 保持 `obu-<domain>-<slug>-<unique>`。
删除 taskDirName 和创建函数的 now 参数，继续使用 MkdirTemp 原子创建及碰撞重试，
不新增 ID 生成器、不承诺后缀固定长度。旧目录和 `.obu.env` 不迁移。
补充无时间戳命名、旧任务真实 Python 续跑、混合命名按文件 mtime 审计的回归；
时间戳只保留于历史 fixture，不参与新任务身份生成。
本次追加验证：命名/并发/旧任务续跑/mtime 专项测试重复 20 次、`make ci`、
`go test -race ./...` 与 `git diff --check` 均通过。

### 提交、合并与收尾

- 实现提交 `14f44ab`（`fix(runtime): harden script transport and task isolation`）
  已按用户要求快进合并到 main。
- 收尾更正文档中的“未合并”状态；确认 review worktree 干净、分支已合并后，
  使用非强制删除清理本轮 worktree 与 `review/obu-flexibility` 分支。
- 另一既有 `agent/sandbox` worktree / 分支保持不动。
- 本次仅文档与 Git 工作区收尾，不再改运行时代码；不推送、不发布、不执行全局
  安装或真实浏览器操作。新版 CLI 本地安装和真机验收仍需后续执行。

### 后续

优先调查 host 慢消费者 backpressure、真实 Chrome/MV3 生命周期与低噪声关联日志；
其后处理 env/memory 行格式边界、事件队列 eviction 指标和旧 plan 归档。
原始 session 内容未写入仓库；历史调用统计仅作启发，不作为当前版本故障率。
