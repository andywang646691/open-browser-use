# Direct CDP Architecture Execution Plan

## 目标

在保持外部 API / CLI / Python / JSON-RPC 协议形状完全不变的前提下，在 Go native host 层引入 Direct CDP 传输层，通过 Chrome `DevToolsActivePort` 连接 Browser Root WebSocket，使用 `Target.attachToTarget(flatten=true)` 驱动标签页 CDP，同时完整保留扩展端现有 CDP 路径作为备份/降级方案。macOS personal-browser 授权采用社区成熟的 launcher-side `mac-approve` 模式：Host 保持单一 patient Browser WebSocket 握手，CLI/Python 客户端通过 `System Events` 精确批准 Chrome sheet 并安全收起 banner。

## 范围

- 包含：
  - 扩展端新增窄作用域精确 `tabId <-> targetId` 映射 RPC（基于 `chrome.debugger.getTargets()` 无需 attach，不使用 URL/title 启发式）。
  - 保留扩展端现有 `attach`、`detach`、`executeCdp`、`onCDPEvent` 逻辑与测试不变作为 fallback。
  - Go native host 拦截 CDP 相关 JSON-RPC（`attach`, `detach`, `executeCdp`）。
  - 支持 `OBU_CDP_MODE=auto|direct|extension` 配置与环境变量（默认 `auto`）。
  - 解析指定 browser/profile 的 user-data-dir 中的 `DevToolsActivePort`，建立 Root Browser WebSocket 连接。
  - 多 profile 安全：通过该 profile 的扩展实例获取精确 tabId->targetId，防止跨 profile 误连。
  - Session / Target 管理：Target.attachToTarget(flatten=true) 懒加载会话、Target.detachFromTarget 精准收尾。
  - 标头/请求多路复用、超时隔离与晚到响应隔离（quarantine）。
  - CDP 事件双向转换：将 direct target session 的 CDP 事件封装为现有 `onCDPEvent` 通知格式。
  - macOS launcher-side `mac-approve`：Direct-capable 请求写入 socket 后，由 CLI/Python 客户端启动隐藏 helper，通过 `System Events -> Chrome windows -> sheets` 精确批准中英文 remote-debugging sheet，并安全关闭 banner；native host 不承担 Accessibility。
  - 单一 patient Browser Root 握手：daemon 只保留一个 pending WebSocket 初始化；caller 超时不取消共享握手、不制造重复授权框；批准后长期复用，Browser 断线后懒重建。
  - 单元测试与端到端测试覆盖：模式选择/降级、精准映射、请求多路复用与超时、事件转发、断连清理、client-side approval、单一 pending handshake。
  - 文档更新与 history 沉淀。
- 不包含：
  - 删除扩展端对 tabs, tabGroups, history, storage, downloads, cursor sidecar 的控制权。
  - 修改外部 CLI / Python helper / JSON-RPC 参数及返回结构。

## 背景

- 相关文档：
  - `docs/ARCHITECTURE.md`
  - `docs/RELIABILITY.md`
  - `docs/SECURITY.md`
  - `docs/design-docs/tab-ownership.md`
- 关键源码：
  - `apps/chrome-extension/background.js`
  - `internal/host/relay.go`
  - `cmd/open-browser-use/main.go`
  - `cmd/open-browser-use/script_helpers.py`

## 风险与缓解

- 风险 1：Chrome 多 profile 运行时，Root DevTools WebSocket 能够看到所有 profile 的 target，可能跨 profile 错连。
  - 缓解：必须通过当前 profile 扩展实例的 RPC 获得 exact `tabId -> targetId` 映射，未在该扩展中声明的 tabId 绝对不路由。
- 风险 2：Direct CDP 请求超时如果假装取消底层 Chrome 操作可能引发竞争。
  - 缓解：遵从现有 RELIABILITY 契约，超时仅清理本地 pending 表，不声称取消 Chrome；晚到响应进入隔离队列抛弃。
- 风险 3：macOS 自动授权误点“在设置中关闭”，或把 Accessibility 权限错误绑定到频繁重建的 native-host binary。
  - 缓解：采用 launcher-side `System Events` helper；只在精确 remote-debugging sheet 中按 `Allow` / `允许`，banner helper 显式拒绝 `设置` / `setting` / `disable` / `turn off`。native host 不请求 Accessibility。
- 风险 4：DevToolsActivePort 不存在或 Browser Root 握手等待授权时被 caller timeout 反复重试，制造多个 pending 连接/授权框。
  - 缓解：daemon-wide 单一 patient 握手；transport 不设置独立 handshake timeout，caller deadline 只结束调用方等待。`auto` 模式在不可用时保留扩展 fallback；显式 `direct` 模式明确报错。

## 里程碑

1. 扩展端新增精确映射 RPC（`getTargetMapping` / `getTargets`）。
2. Native host 层 Direct CDP 核心引擎（DevToolsActivePort 解析、WebSocket 客户端、Target 多路复用与事件路由）。
3. 传输模式选择器（auto, direct, extension）与降级机制。
4. macOS launcher-side `mac-approve`（System Events sheet 批准 + safe banner dismiss）与 daemon-wide patient Browser handshake。
5. 全套回归测试与集成测试验证。
6. 文档更新、历史记录沉淀与 `make ci` 验收。

## 决策记录

- 2026-09-12：精确 Profile 选择拒绝默认猜测：Native Host 在初始化 Direct CDP 时，通过内部 `getInfo` RPC 获取当前连接的 exact extensionId + extensionInstanceId，经 LevelDB 查找精确定位其 user-data-dir；严禁默认回落到 Chrome Stable root。无法精确定位时，`auto` 回退至扩展模式，`direct` 显式报错。
- 2026-09-12：迟到 Attach 自动回收与超时竞争消除：在 Client 请求层增加 LateResponseHandler。在 context.Done 超时分支原子检测 pending 状态；若已被 readLoop 消费则取走正常返回避免虚假超时与会话泄漏，若仍 pending 则注册 lateHandler；Chrome 稍后返回 sessionId 时自动触发异步 `Target.detachFromTarget` 回收迟到会话。
- 2026-09-12：调用方时限透传：基于传入的 `_deadlineUnixMs` 创建带 Deadline 的 Request Context，并在调用内部 `getTargetMapping` 时显式注入 `_deadlineUnixMs`，尊重调用方时限预算。
- 2026-09-12：保持事件 shape 一致：针对标签页事件，`onCDPEvent` 的 `source` 严格保持 `{ tabId }`，不附带扩展端未包含的 `targetId` 字段。
- 2026-09-12：防止重入扩展调试器与文件选择器完整实现：在 Direct 会话中通过 `Page.setInterceptFileChooserDialog` 监听事件，存储 `backendNodeId`、`mode` 及所有者 `sessionId`，并在 `SetFileChooserFiles` 中发送 `DOM.setFileInputFiles`（保留 backendNodeId、多文件校验与会话隔离），并在完成后/超时/取消时在 defer 中禁用拦截。剪贴板（含 structured `readClipboard` / `writeClipboard`）在 Direct 模式下直连 Direct CDP 会话，并审计 `Runtime.evaluate` 的 `exceptionDetails` 异常。
- 2026-09-12：清理绝不误触发 Direct CDP 初始化：`releaseTabs`、`closeUserTab`、`finalizeTabs`、`turnEnded` 仅快照已有的 Direct Manager；若未初始化 Direct CDP 则绝不发起 `getInfo` 或连接 Chrome。
- 2026-09-12：路由在 Detach 后保持 Sticky：标签页 detach 仅断开 Direct CDP Target 会话，不从 `tabRoutes` 移除路由，重连时依然沿用 direct 路由，仅在 releaseTabs/closeUserTab/finalizeTabs/turnEnded 时清除。
- 2026-09-12：按社区 `browser-harness` 的 personal-Chrome 实践改造授权路径：废弃 native-host 内的 darwin/cgo AX walker，改由 CLI/Python launcher 在请求写入 socket 后启动 `System Events` `mac-approve`；原因是真机验证中 unsigned native-host 反复重建会造成 TCC identity churn，且大 Chrome UI tree 的原生 AX 扫描不稳定。launcher-side `windows -> sheets` 已在当前中文 Chrome 真机自动点击 `允许` 成功。
- 2026-09-12：Relay Manager 初始化升级为 daemon-wide singleflight/patient handshake：本地 Browser Root WebSocket 不设置独立 handshake timeout；多个 caller 共享同一 pending 初始化，caller deadline 不取消共享握手。授权后长期复用，Browser WebSocket 断开后下一次请求丢弃 stale Manager 并懒重建，tab route 保持 sticky。

## 进度记录

- [x] 里程碑 1：扩展端精确映射 RPC（`getTargetMapping`，要求 session_id/turn_id/tabId，返回 `{ tabId, targetId }`，无跨 profile 枚举）
- [x] 里程碑 2：Native Host Direct CDP 引擎（`internal/cdp`，DevToolsActivePort 解析、WebSocket 客户端、Target 多路复用与事件路由、迟到 Attach 自动 detach 回收）
- [x] 里程碑 3：模式选择与降级（`OBU_CDP_MODE=auto|direct|extension`，CLI / Python 隐式传递 `_cdpMode`，per-attachment route 锁定，exact profile 定位拒绝默认回落）
- [x] 里程碑 4：macOS launcher-side `mac-approve`（System Events 精确 Chrome sheet、Allow/允许、中英文支持、安全 banner dismiss）+ daemon-wide patient Browser handshake；native host 不承担 Accessibility。
- [x] 里程碑 5：测试覆盖（覆盖模式选择/降级、精准映射、超时隔离、迟到 attach detach、事件转发、断连清理、client approval、单一 pending handshake）并完成 Direct/Extension 双路真实 Chrome E2E。
- [x] 里程碑 6：文档与 CI（ARCHITECTURE/RELIABILITY/SECURITY 已更新，沉淀 history 并通过 `make ci`）
