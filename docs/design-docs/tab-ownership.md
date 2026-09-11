# Tab ownership：任务、控制权与页面分离

## 契约

Task workspace 保存跨轮脚本、进度、browser/profile 与 session 身份，不意味着
长期独占页面。Chrome 页面可以在零 owner 状态下继续存在。

- `claim_tab(id)` / `claimUserTab`：无 owner 时领取；同 owner 幂等；其他 owner
  报错，wire error.data 包含 reason=tab_owned、tabId、ownerSessionId。
- `release_tabs(ids=None)` / `releaseTabs`：无参数释放当前 session 全部 ownership；
  空数组不操作。只释放，不关闭、不移动、不解组，不删除工作区。
  返回 released/alreadyReleased/failed/remaining；部分失败不伪装成功。
- `close_tab(id)` / `closeUserTab`：必须当前 session 拥有，关闭并清除追踪。
  与历史行为不同，不再作为无 ownership 的任意关闭逃生舱。
- `mark_deliverable(id)` / `markDeliverable`：必须拥有，将成果放进共享成果组，
  隔离窗口有可用用户窗口时移出，不抢焦点；ownership 不变。

显式 tabOrigins 是唯一 ownership 来源，不按 tab group 或隔离窗口推断成员。
原有持久化 tabOrigins 可直接沿用；原来没有记录的、用户手动放进任务组/窗口
的页面不会自动被领走。getUserTabs 列出所有真实页面并附 ownerSessionId。

ownership 在同一 turn 的多个 exec 脚本间保留；等待用户输入和正常本轮结束前
释放。下一轮沿用工作区，重新 claim、观察，不假设页面未被用户修改。
工具不能感知上层 Agent turn 边界，因此不会在每次 Python 进程结束时自动释放。

## 收尾

先 mark_deliverable(result)，close_tab(自己的临时页)，最后 release_tabs()。
只读来源页不自动视为成果：在聊天中交付调研结果时通常只需 release 原页面。
成果移走后若任务组仍存在，先查看真实成员；禁止按组名/空白 URL 自动扫除。
首次 claim 创建隔离窗口时立即移除本次自建启动页，不再把它留给 finalize。
未完成页面不关闭即可，无 handoff/end_turn 状态。release-all 的 failed 和
remaining 都为空才算成功。不得关闭用户原页，除非用户明确要求。

释放成功后旧 session 的 tab-scoped 操作会被拒绝；不同任务可重新 claim。
claim/release/close/mark 与 attach/CDP/文件写入的关键转移在扩展内串行，防止
异步 Chrome API 的检查与写入间出现双 claim。长事件等待不占用该队列。
相同 session ID 的多个执行者仍需上层协调，ownership 不是本地安全沙箱。
队列等待与 attach/readiness 有界，过期排队操作不执行；未决 Chrome attach 的
迟到清理结束前保留该 tab ownership，不会因 timeout 让其他 session 接管。
详见 [readiness/队列契约](../RELIABILITY.md#后台页面-readiness-与排队过期)。

## 兼容与异常

finalize_tabs/finalize-tabs/finalizeTabs 暂留弃用兼容，发出弃用提示，并从新
API sheet/Skill 主流程撤下。旧 handoff 仍保留 ownership，不静默改变语义。
legacy turnEnded 仍只清理 active-turn/debugger，不是 release。

旧扩展不支持新 wire 方法：需重载更新的扩展，不能用旧 handoff fallback。
旧 session 有残留时，确认原执行者已停止后，由其任务身份显式 release；不能
仅因看到 ownerSessionId 就复用它绕过冲突。无 force-claim、lease 或超时回收。
崩溃/漏调用仍可能留下 ownership，查询 owner 并报告；自动回收不在本期范围。

释放时 debugger detach 或持久化失败保留 ownership 并报告失败。页面已经被
关闭时可释放残留记录。成果整理失败不自动释放，先观察实际页面再决定恢复。
raw CDP 保留强能力，不把本次协作约束描述成恶意本地客户端的安全隔离。

## 验证

extension tests 覆盖重新领取、不同 owner 拒绝、并发 claim、MV3 store 重建、
隔离窗口释放不移动、无关页面不进入 ownership、mark 不释放、重复 mark、
detach 失败保留 ownership、已关闭页面清理，以及 legacy finalize 兼容。
Python tests 覆盖参数、release-all/空数组和当前 tab 指针；Go/API sheet 与
仓库 CI 覆盖公开入口。真实 Chrome 验收需专用测试页，不接触私人后台。
