# macOS 授权助手加固

## 目标

修复 2026-10-20 代码审查发现的 `mac-approve` 链路问题，使 Direct CDP 在 personal Chrome 上的远程调试授权和 automation banner 收起在 CLI、Python（`obu exec`）、MCP 三种入口下行为一致、可观测、不打扰用户。

## 审查发现（修复前）

1. CLI 是一次性进程：批准后的 banner 收起由 CLI 内 goroutine 触发，CLI 返回后即丢失。
2. Python 路径从不收起 banner。
3. 无辅助功能权限时 helper 弹出阻塞式 `NSAlert`：Python 无超时永久挂起且逐请求叠加；Go 45s 杀掉后误记为 timeout，exit 3 分支几乎不可达。
4. 已批准后每个 direct-capable 请求仍启动 30s 空转轮询（每 120ms 一次 AppleScript `entire contents` + 整窗 AX 遍历），并发叠加；`OBU_MAC_APPROVAL_PARENT_PID` 已传入但 helper 从不读取。
5. 原生兜底遍历整窗 AX 树（含网页 `AXWebArea`），网页内 `role=alertdialog` 会被当成授权框点击 `Allow`。
6. banner 以“信息栏容器”为粒度，可能点到其他 infobar 的关闭按钮。
7. AppleScript 路径：helper bundle 缺 `NSAppleEventsUsageDescription`、只认 `Google Chrome`、只看 `window 1`、非 `not-found` 结果跳过原生兜底。
8. Python 以环境变量而非请求实际 `_cdpMode` 判断。
9. 死代码（`runMacRemoteDebuggingBannerHelper`、`open-browser-use-mac-approval-helper.sh`）与文档漂移。
10. 新发现：helper 由 CLI/agent 直接 exec 时，TCC 以“负责进程”（终端/agent）判定权限，用户在设置中启用 helper 本身并不生效。真机对照：同一 helper disclaim 后 `trusted=1`，继承 AgentDock 身份时 `trusted=0`。
11. 真机新发现：Chrome 把授权框挂到最近激活的浏览器窗口，常常是已安装 Web App（PWA）窗口；这些窗口由 `com.google.Chrome.app.<id>` 应用壳进程承载，只扫描 `com.google.Chrome` 主进程时找不到授权框。旧实现同样只扫主进程。
12. 真机新发现：一次性 CLI 默认 10s 超时，“调用方退出即停止”会留下仍在屏幕上的授权框；改为调用方退出且无可见授权框时才停止。

## 方案

- helper 只保留原生 AX 实现，删除 AppleScript 路径。
- helper 启动时用 `responsibility_spawnattrs_setdisclaim` 重新 exec 自身，使 TCC 只认 helper bundle。
- 新子命令 `approve-then-dismiss-wait`：批准与 banner 收起在同一 detached helper 内串联；批准阶段读取调用方 PID，调用方退出即停止；banner 阶段独立于调用方。
- 批准与收起各有单实例锁，并发请求直接退出（exit 4）。
- 后台模式不弹任何阻塞 UI：未授权时立即 exit 3，系统授权提示每个 helper 构建只触发一次；`setup` 子命令承担交互式引导；`status` 只读检查；`dump` 输出浏览器原生 UI 的 AX 树用于排障。
- 授权框只在 window 的 sheet 与 `AXDialog` 窗口中查找，并跳过 `AXWebArea`；扫描 Chrome 主进程与 PWA 应用壳进程。
- helper 自己写 JSONL 诊断（阶段开始、看到授权框、按下 / 按下失败含 AXError、退出码），`probe` 子命令只报告不点击。
- banner：先定位含 automation 文案的元素，沿 `AXParent` 向上找到最近的含关闭按钮的 infobar，遇到信息栏容器即停止。
- Go / Python 启动侧：请求写入后延迟 400ms 仍无响应才启动 helper（已授权时请求很快返回，不再空转）；Python 按请求实际 `_cdpMode` 判断；helper 以独立 session 启动。
- `self-test` 子命令覆盖文本匹配逻辑，由 Go 测试在 macOS 上编译并运行。

## 退出码契约

`0` 已批准/已收起；`1` 超时未找到；`2` 参数错误；`3` 无辅助功能权限；`4` 已有同类 helper 在运行；`5` 调用方已退出且无可见授权框。

## 验证

- `go test ./...`、`python3 -m unittest`（script helpers）、helper `self-test`。
- `make local-install` 后：`status`/`dump` 真机检查；用真实 Browser WebSocket 连接触发 Chrome 授权框，确认自动批准与 banner 收起；Python 与 CLI 两条入口各验证一次。

## 风险

- ad-hoc 签名的 helper 每次重建后 TCC 授权失效，需要用户重新启用；配置 `OBU_MAC_CODESIGN_IDENTITY` 可避免。
- `responsibility_spawnattrs_setdisclaim` 为私有 API（Chromium/LLDB 同样使用）；不可用时退回进程内执行。

## 进度

- [x] helper 重写
- [x] Go / Python 启动侧
- [x] 构建安装与真机验证
- [x] 文档与 history

## 真机验证结果（Chrome 155，zh-CN，2026-10-21）

- CLI `obu list-extensions`（新 Browser Root 握手）：请求后 0.44s 启动 helper，在 Claude PWA 窗口上按下“允许”，Direct CDP 连接建立，1.59s 返回；CLI 退出后 helper 继续收起 automation infobar。
- Python heredoc `list_extensions()`：同样自动批准并收起 infobar，3.8s 完成。
- 已批准后连续 3 次 CLI 请求各约 0.15s，`mac_approval_helper_started` 为 0。
- infobar 收起后其他扩展按钮（文案含 “automated captcha solver”）未受影响。

## 遗留

- ad-hoc 签名每次重建需重新授权；长期方案是配置 `OBU_MAC_CODESIGN_IDENTITY`。
- 被关闭客户端遗留的孤儿授权框若先被批准，host 的 patient 握手可能卡住且不再弹框，需要重载扩展；属于 host 握手策略，未在本次处理。
