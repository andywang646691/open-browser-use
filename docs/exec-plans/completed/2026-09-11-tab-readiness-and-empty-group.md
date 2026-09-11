# 后台页面 readiness 与空壳任务分组清理执行计划

## 目标与背景

在 Herdr pane `wH:p1` 的真实任务运行中暴露两个问题：
1. 成果 tab 移入 OBU 完成组后，原始任务 tab group 遗留了一个包含 `about:blank` 的空壳 tab group；
2. claim 到闲置/冻结标签页时 CDP attach 超时，导致后续排队操作级联超时，最终需要用户手动激活页面才能恢复。用户要求由 OBU 内部处理闲置/冻结标签页 readiness，不需要外部 agent 关注。

## 根因与机制

1. **队列级联挂起机制**：
   - 原始运行记录并未直接捕获 incident 发生时的 `frozen`/`discarded` 状态标志。
   - 确立的失效机制为：未决的 `attach`/wake 导致扩展内全局 `ownershipQueue` 阻塞；
   - 外部 Python 脚本在 10s socket timeout 后退出，但发出的 `createTab` 已经进入队列；
   - 当 Chrome 底层恢复后，排队的 `createTab` 迟到执行，生成未被导航的 `about:blank` 孤儿页并加入 `Task - OBU` 组。
   - Frozen 与 Discarded 是 Chrome 在后台节流或 Energy/Memory Saver 下已知且合理的生命周期状态，现已由 OBU 内部接管，并在合成专用测试页上验证了 frozen 恢复，但不可反推为原事件当时的既定事实。
2. **空壳任务组遗留机制**：
   - 迟到执行的 `createTab` 遗留了未导航的 `about:blank` 孤儿页；
   - 随后的收尾脚本将真实网页（如 X 首页）通过 `mark_deliverable` 移入 `✅ Open Browser Use`；
   - 此时 `Task - OBU` 组内仅剩该孤儿 `about:blank`，无法被 Chrome 自动解散，遗留为空壳 tab group。

## 设计与实现

- **内部 Readiness 管理**：
  - `attach` 前通过 `chrome.tabs.get` 检查页面状态：若 `tab.discarded`，后台调用 `chrome.tabs.reload(tabId)`；
  - 调整唤醒顺序：先通过 CDP `Page.setWebLifecycleState({state: "active"})` 解冻 renderer，再调用 `Emulation.setFocusEmulationEnabled(true)`；
  - `attach` 内置 `readinessTimeoutMs`（6s），超时后阻止后续操作执行，并在迟到 attach 返回时主动 detach，避免污染会话状态；
  - 正在处于未完成 readiness 状态的 tab 进入 `pendingReadiness` 隔离，在此期间禁止并发 claim 或不安全的 release。
- **队列过期与独立 TTL 保护**：
  - CLI 和 Python prelude 自动在 JSON-RPC 参数中注入 `_deadlineUnixMs`；
  - 扩展在 `ownershipQueue` 中严格执行 `wait <= min(queueTimeoutMs, remaining caller deadline)`（默认 queue TTL 为 2s）；
  - 即使调用方设置了长 deadline，排队的副作用操作（如 `createTab`）在队列中滞留超过 2s 也直接废弃报错，绝不在客户端断连前夕盲目启动，从根源消除迟到生成幽灵 `about:blank` 的漏洞。
- **孤儿空白页根除与 Contract 边界守恒**：
  - `open_tab(url)` 在 Python 和 CLI 中若 attach/navigate 失败，自动通过补偿清理刚刚创建的临时 tab；
  - `moveTabIntoSessionWindow` 遇到 `chrome.windows.create` 产生的启动初始 `about:blank` 立即清理；
  - 坚守 `tab-ownership.md` 契约：`markDeliverable` 仅组织指定的单个成果页，不根据 URL 或分组推测扫荡其他 agent 标签页（Agent 可能合法持有并发的 `open_tab("")` 空白页），绝不误删任何用户或会话标签页。
- **指引与契约优化**：
  - 更新 `SKILL.md` 和 `tabs.md`，明确 OBU 内部处理 readiness，Agent 不需要手动激活、刷新或建替代页；明确调研只读来源页通常只需 release，不需要调用 `mark_deliverable`。

## 验证与交付

- [x] 单元与契约测试：`tab-readiness.test.mjs` 覆盖 discarded 后台 reload、resident frozen 内存保活解冻、hung attach 超时隔离与恢复、队列 2s TTL 丢弃、长 deadline 仍受 queue TTL 约束、迟到 attach detach、debugger conflict 拒绝、MV3 重启自身 attachment 识别、`markDeliverable` 保留其他 agent/用户空白页等 22 项测试。
- [x] 回归测试：`move-to-deliverables.test.mjs`、`isolated-window.test.mjs`、`background-wake.test.mjs`、`script_helpers_test.py` 全数通过。
- [x] 全量 CI：`make ci` 通过（含 Go、Python、Extension、SDK、Package 校验与代码规范）。
- [x] 本地部署：`make local-install` 更新 CLI 及仓库 linked binary，验证 `type -a obu` 及其调用路径。
- [x] 真实 Chrome 验收：`scripts/tab-readiness-smoke.py` 在真实 Chrome 专用 fixture 上验证冻结页内部唤醒、内存标记保活、成果转移，并精确断言本轮运行未向 `Task - OBU` 泄漏任何新标签页，且不干扰用户既有标签页。
