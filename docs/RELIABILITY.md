# 稳定性与可运维性

## 公开运行路径与灵活性边界

当前 Agent 主路径为 `taskinit → exec → finalize_tabs`。CLI 便捷命令、
`run`、heredoc、任意 JSON-RPC `call` 与原始 `cdp()` 均保留；没有新增
站点 allowlist、DOM DSL、自动点击/重试或强制任务编排器。

helper 签名与返回契约以 `obu api-sheet` / 脚本内 `help(name)` 为准。
Go CLI 负责 socket/profile 发现；Python 标准库负责同步 frame 传输；
extension 继续负责 Chrome 生命周期。安全边界见 [SECURITY.md](SECURITY.md)。

## Python 文件执行契约

```sh
obu exec path/to/task.py -- --limit 20 "argument with spaces"
```

- `__file__` 是任务文件绝对路径，`__name__ == "__main__"`；traceback 行号
  是原文件行号，同目录模块可直接 import。
- `--` 后的参数原样传给 `sys.argv[1:]`，不会当成 OBU flag 或 shell 代码。
- 保持调用方 cwd；读写脚本旁边的数据请用 `Path(__file__).parent`。
- helper 全量预导入（包括原有单下划线名称），完整 Python 语言不变。
- stdout/stderr/退出码保持透传。`.obu.env` 仍只从脚本同目录读取，或显式
  `--env-file` 指定；session/browser/profile 的 flag 优先于文件，再优先于继承环境。
- taskinit 输出已经包含 api-sheet，新任务无需为同一份表再发起一次调用。

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

本轮复审未重载插件、操作网页或替换全局安装；真实 Chrome、MV3 重启、
多客户端慢消费者与跨层日志仍需专项验收。后续事项见
[技术债追踪](exec-plans/tech-debt-tracker.md)，流水线见 [CICD.md](CICD.md)。
