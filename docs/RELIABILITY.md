# 稳定性与可运维性

## 公开运行路径与灵活性边界

当前 Agent 主路径为 `taskinit → exec（claim/操作/标记成果/关闭临时页）→ release_tabs`。
后续 turn 复用原 workspace，但重新 claim 并观察。release 只归还控制权，
不关闭页面；检查 failed/remaining，部分清理失败必须报告。旧 finalize_tabs
仅为弃用兼容，handoff 不会释放。详见 [ownership 契约](design-docs/tab-ownership.md)。CLI 便捷命令、
`run`、heredoc、任意 JSON-RPC `call` 与原始 `cdp()` 均保留；没有新增
站点 allowlist、DOM DSL、自动点击/重试或强制任务编排器。

helper 签名与返回契约以 `obu api-sheet` / 脚本内 `help(name)` 为准。
Go CLI 负责 socket/profile 发现；Python 标准库负责同步 frame 传输；
extension 继续负责 Chrome 生命周期。安全边界见 [SECURITY.md](SECURITY.md)。

## Python 文件执行契约

```sh
obu exec --task x.com/fetch-a8k3 task.py -- --limit 20 "argument with spaces"
```

- `taskinit` 输出 `taskRef=<domain>/<instance>`、task root 与绝对 task `dir`。
  文件工具写入绝对 `dir`；执行工具使用 taskRef，因此调用方 cwd 不参与任务定位。
  `exec --task` 默认按 `$OBU_TASK_DIR` / `~/.open-browser-use/tasks` 解析，非默认
  root 用 `--task-root` 显式传入。旧 `obu exec <path>` 保持兼容。
- `__file__` 是任务文件绝对路径，`__name__ == "__main__"`；traceback 行号
  是原文件行号，同目录模块可直接 import。
- `--` 后的参数原样传给 `sys.argv[1:]`，不会当成 OBU flag 或 shell 代码。
- Python 子进程的 cwd 自动切换到脚本所在的 task workspace；调用方进程的 cwd
  不受影响。脚本可直接使用相对路径读写任务目录内的数据。
- helper 全量预导入（包括原有单下划线名称），完整 Python 语言不变。
- stdout/stderr/退出码保持透传。`exec --task` 从解析出的 task workspace 加载
  `.obu.env`；旧路径模式仍只从脚本同目录读取。两者都可由 `--env-file` 显式
  覆盖；session/browser/profile 的 flag 优先于文件，再优先于继承环境。
- `taskinit` 是新任务 bootstrap：除 api-sheet 外，只输出 normalized host 与任务 domain 精确匹配的用户 tab（含 `ownerSessionId`），以及匹配 site-memory 和显式链接的 technique/runbook（有总量/单文件预算并标记截断）。它不 claim tab、不执行 runbook；bootstrap 足够时无需再调用 `user_tabs()` 或重复 grep/read memory。

## 请求与事件恢复

| 情况 | 行为 | 调用方处理 |
| --- | --- | --- |
| 等待响应时收到 notification | 加入 500 帧的有界 FIFO 尾队列，按 id 匹配响应 | 无需重复发命令 |
| `wait_for_event` 遇到非匹配 method/predicate | 保留在队列，只移除第一个匹配事件 | 可按 requestId/tabId 连续消费不同事件 |
| 等待超时或 predicate 异常 | 恢复进入调用前的 socket timeout；非匹配事件保留 | 修正条件或继续观察，不自动重放动作 |
| header/body 读到一半超时 | request/wait 共用的接收缓存保留半帧 | 下一次读取继续该帧；迟到响应不会误配新请求 |
| 通知持续到达 | 每次 request 使用绝对 deadline，不为每个 recv 重新计时 | `--timeout 250ms` 等小数秒精度保留 |
| 首次 connect 失败 | 关闭候选 socket，不缓存坏连接 | 可在目标恢复后显式再试 |
| sendall 失败/中途超时 | 发送进度未知，关闭连接并拒绝本进程后续请求 | 新脚本先检查浏览器状态，再决定是否重试 |
| socket 关闭/协议损坏/超大帧 | 报错；帧长度上限与 Go 一致为 64 MiB | 检查 host，重启脚本；不是透明重连 |

事件队列不是持久订阅：进程结束就消失，满 500 帧时丢弃最旧项；高事件量
抓取应尽快消费，或使用独立的上层订阅实现。保留非匹配事件意味着旧事件也可能
命中下一次 wait，predicate 应按本次 requestId、source.tabId 等显式限定。
默认不替调用方加 tab 过滤，跨 tab 观测能力保留。

同一 Python helper 连接不支持并发/重入请求，不要在另一线程或 predicate 内
调用 browser helper。wait timeout 限制 socket 等待，不能中断用户 predicate
中的阻塞代码。事件诊断的 URL 去除 userinfo、query、fragment，但 path 仍可能
敏感；不要直接分享含浏览数据的错误日志。

### 读取网络响应体

在同一脚本内启用 `Network`、触发动作、等待 response、再等待同 requestId
的 `Network.loadingFinished`，然后 `Network.getResponseBody`。
`responseReceived` 表示响应可用，不表示响应体已经接收完；失败、流式响应、
缓存失效/导航都仍需调用方处理。具体示例见 `help(wait_for_event)`。

此顺序依据 [CDP Network 契约](https://chromedevtools.github.io/devtools-protocol/tot/Network/)，
不是新的 HTTP 抓取封装，不解析或修改站点返回值。

## Direct CDP 传输稳定性与有界恢复

- **单一 patient Browser 握手**：personal Chrome 在首次 Browser Root WebSocket 连接时可能把 HTTP Upgrade 停在“Allow remote debugging?”授权框。Direct client 不设置独立 handshake timeout；Host 在 daemon 范围仅创建一条 pending 初始化连接。caller `_deadlineUnixMs` 到期只结束该 caller 的等待，不取消共享 Browser 握手，因此不会因重试制造第二条连接或第二个授权弹窗。批准后所有 task 复用同一 Browser Root 连接。
- **客户端侧 macOS 授权**：CLI / Python prelude 先把 Direct-capable JSON-RPC 请求写入 Unix socket，再启动安装到 `~/Library/Application Support/OpenBrowserUse/approval-helper/` 的稳定、代码签名的 `Open Browser Use Approval Helper.app`。该 helper 是唯一执行 Accessibility API 的进程，首次运行调用 `AXIsProcessTrustedWithOptions` 请求系统显示辅助功能授权提示；用户只需给这个 helper 一项辅助功能权限。CLI、Python、native messaging host 和 Chrome 不承担 Accessibility 权限。helper 精确寻找中英文远程调试 sheet，只点击 `Allow` / `允许`；批准后复用同一个 helper 收起 automation banner。每次 direct-capable 请求、helper 启动、实际 sheet 点击、Direct CDP 重连/失败和 helper 超时都会写入本地 JSONL 日志 `~/Library/Logs/OpenBrowserUse/native-host.log`（可用 `OBU_LOG_FILE` 覆盖）。
- **Browser 断线恢复**：已建立的 Browser Root WebSocket 关闭后，Manager 被视为 stale；下一次 Direct 请求清掉旧 transport manager 并建立新的单一 patient 握手。标签页的 direct/extension route 选择保持 sticky，新 Manager 会按原 route 懒 attach，不静默改走另一条 transport。
- **有界超时与隔离（Quarantine）**：Direct CDP 执行请求在本地具有有界超时（默认 10s 或 caller 指定的 timeoutMs / _deadlineUnixMs）。本地超时仅剔除并隔离本地 pending 请求，不声称取消了 Chrome 底层动作；超时后迟到的 Chrome 回复被安全丢弃，不污染后续请求。
- **迟到 Attach 自动回收**：当 `Target.attachToTarget` 超时后如果 Chrome 在稍后回复了新的 sessionId，Host 会触发 best-effort `Target.detachFromTarget` 立即释放该迟到 session，杜绝底层 session 泄露。
- **主 frame 导航异步唤醒重放**：当接收到 main frame 的 `Page.frameNavigated` 事件时，Host 异步启动后台唤醒重放（`Page.setWebLifecycleState(active)` 与 `Emulation.setFocusEmulationEnabled(true)`），不阻塞 WebSocket 读取主循环。
- **生命周期同步收尾**：任何 `releaseTabs`、`closeUserTab`、`finalizeTabs` 或会话断开动作均显式调用 Direct CDP Detach 回收目标 session，确保与 Chrome 扩展端所有权严格对齐。
- **Extension 生命周期**：`listExtensions`、`installExtension`、`reloadExtension`、
  `triggerExtensionAction`、`uninstallExtension` 使用同一条 browser-root Direct CDP
  连接。`triggerExtensionAction` 未给出 `tabId` 时，Host 先向扩展查询当前
  session 的唯一逻辑 active tab；若没有或有多个 active tab 则失败，不猜测页面。
  这些方法没有 page-tab attachment，因此 `releaseTabs` 不会误清理 Extension
  域操作；trigger 为解析 synthetic tab target 会短暂 attach tab session，
  成功或失败都会 best-effort detach；Chrome 不支持 `Extensions.*` 时向调用方返回明确错误。

## 后台页面 readiness 与排队过期

OBU 扩展负责页面生命周期，不把“请用户激活闲置标签页”当常规抓取步骤：

- `attach` 先检查 tabs.get：discarded 页面没有可执行 renderer，后台 reload；
  resident/frozen 页面保留内存状态，不刷新、不激活，不改变窗口焦点。
- 先 CDP `Page.setWebLifecycleState(active)`，再 focus emulation。旧顺序可能
  在 renderer-dependent focus 命令挂起，永远到不了自己的解冻步骤。
- attach 与 wake 共用默认 6s deadline；导航重放同样有界。Chrome Promise
  不能取消，超时后不续跑后续 wake 或用户 CDP，迟到完成后 detach。
  未完成 cleanup 的 tab 被隔离为 pending，拒绝再次操作/释放，ownership 保留；
  其他 tab 可继续工作。页面已经关闭时可清理 ownership。
- Chrome 拒绝不支持的 wake 命令仍 best-effort；无响应则超时报错，不能把
  Promise.race 宣传为 Chrome 动作已取消。无响应的底层 Promise 仍可能需要
  Chrome 恢复/扩展重载，不能自动抢焦点或删除页面。
- 全局 ownership queue 等待最多 2s，过期请求报错且永远不再启动。
  CLI/Python 内部发送 `_deadlineUnixMs`（本机时钟），也拒绝本地超时后才出队的
  请求；raw/内部 SDK 无该字段仍受队列默认时限保护。它不是已开始动作的取消协议。
- 光标 ping/注入/发布各最多 300ms，辅助 UI 不能无限拖住 claim/create。

回归覆盖 hang/late attach、生命周期顺序、discarded、过期 create、pending
释放、MV3 旧 attachment 与其他 debugger 拒绝；`scripts/tab-readiness-smoke.py`
用专用页面冻结后普通 evaluate 自动恢复，并验证内存标记不丢失、收尾释放。
2026-09-11 重载扩展后此真实 Chrome smoke 已通过。discarded 重载和卡死 Chrome
Promise 的迟到行为目前由 fake 验证，不等同于所有节能策略的真机验收。

## 任务隔离

- 目录为 `<domain>/<slug>-<unique>/`，通过 MkdirTemp 原子创建及碰撞重试
  防止并发复用，session 包含 domain 与完整实例名。unique 是标准库生成的
  不透明后缀，不依赖固定长度；目录不再附加时间戳，也不承诺按名称按时间排序。
- `tasksizes` 的闲置判定读取任务内文件的最新 mtime，不解析目录名中的时间；
  新旧命名格式可以共存，体积审计及闲置判定不变。
- 旧任务及 `.obu.env` 不迁移、不重命名，继续用原文件 exec 即可。
- domain/slug 校验目录层级、控制字符和保留名；不限制为公网 DNS，
  localhost、内网主机名、Unicode 域名保留。domain 层 symlink 被拒绝。
- taskinit 使用 browser **和** profile 过滤；多匹配仍要求用户选。
  保存完整 browser target（包含实例），无法映射 profile 时用 extension
  instance id 锚定，不默默回落到 active registry。
- 显式提供完整 browser/profile 但离线时，仍可创建工作区并显示不可达警告。

## 验证与剩余缺口

`make ci` 通过 `scripts/ci.sh` 运行 Go、extension/JS package、Python SDK 及
`cmd/open-browser-use/*_test.py`。后者包含 fake host 与 socketpair 的半帧、
超时、predicate、乱序事件回归，不能替代真实 Chrome smoke。

真实 Chrome 专用冻结页 smoke 已验证；MV3 pending 状态恢复、
多客户端慢消费者与跨层日志仍需专项验收。后续事项见
[技术债追踪](exec-plans/tech-debt-tracker.md)，流水线见 [CICD.md](CICD.md)。
