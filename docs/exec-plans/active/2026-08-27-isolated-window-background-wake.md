# 隔离窗口后台节流唤醒 (Isolated-Window Background Wake)

## 目标

隔离窗口里的会话 tab 在 debugger attach/导航时，自动应用 CDP 层唤醒
（`Emulation.setFocusEmulationEnabled(true)` + `Page.setWebLifecycleState({state:"active"})`），
让被遮挡窗口的页面的懒加载/渲染管线不再被 compositor 节流挂起——不抢焦点、
不打扰用户，detach 即复位。DOM 注入层（`session_inject("visibility")`）保持显式，
本轮不扩展。

## 范围

- 包含:
  - 扩展:`attach()` 对**隔离会话**的 tab 自动应用 CDP 唤醒(focus 仿真 + lifecycle active),
    best-effort、逐命令容错。
  - 扩展:`Page.frameNavigated`(主 frame)对已唤醒 tab 自动重放唤醒——导航会重置
    lifecycle 为 hidden+节流,需要自动维护。
  - 扩展:detach 时清出唤醒追踪集合(focus 仿真随 debugger detach 天然复位)。
  - 扩展:单测新增唤醒行为断言(fake sendCommand 录制 + isolated/user 模式对照)。
  - 文档:SKILL.md、ARCHITECTURE.md、SECURITY.md 同步;history 记录。
- 不包含:
  - `session_inject` 扩层(DOM 注入 + CDP 唤醒合并)——用户明确本轮不做,先要
    真机验证 focus + lifecycle 单独是否足够。
  - user 模式(共享用户窗口)tab 的唤醒——用户窗口本来就可见/聚焦,无需也不该动。
  - `Page.setPageVisibilityOverride` 之类不存在的可见性命令(已实证回滚,见 history)。
  - 常驻 attach(createTab/claim 时立刻 attach)——保持现有 lazy-attach 架构,
    唤醒在首次 CDP 交互的 attach 路径上完成。

## 背景

- 相关文档:`docs/histories/2026-08/20260826-1812-visibility-emulation-empirically-reverted.md`
  (setPageVisibilityOverride 不存在;setWebLifecycleState 单独无法翻转 visibilityState)、
  `docs/histories/2026-08/20260827-0123-session-inject-main-world.md`(DOM 注入验证:
  页高唤醒但帖子数不动)、`docs/exec-plans/completed/2026-08-26-session-inject-main-world.md`
- 相关代码路径:
  - `apps/chrome-extension/background.js`(`attach`/`handleCdpEvent`/`getSessionTabs`/`finalizeTabs`)
  - `apps/chrome-extension/isolated-window.test.mjs`(隔离窗口 fake)
  - `apps/chrome-extension/main-inject.test.mjs`(debugger fake + 录制模式可复用)
  - `skills/open-browser-use/SKILL.md`(当前"无 CDP 可见性仿真"指引)
  - `docs/ARCHITECTURE.md`(隔离窗口模型段落)
- 已知约束:
  - 真机结论:`document.visibilityState` 由窗口遮挡驱动,CDP 无仿真命令;
    occluded 窗口的懒加载被 compositor 节流挂起(滚动无新请求),是渲染器层问题。
  - 用户/上游实证:三层组合(伪可见性 + focus 仿真 + lifecycle active)后 x.com
    懒加载恢复(4→11 篇);本轮先只做 focus+lifecycle 两层,留待真机验证。
  - `chrome.debugger` 的 emulation 状态随 debugger 会话存在,detach 自动复位;
    lifecycle 状态不跨导航持久,需 frameNavigated 重放。
  - attach 是懒触发的(首次 CDP 交互),且 prelude 的 `cdp()`/`evaluate()` 都先
    `_attach()`,唤醒挂 attach 可覆盖全部 agent 交互。
  - 隔离窗口 tab `getSessionTabs` 返回的 tab 可能包含 `chrome://`(已 filter)。

## 风险

- 风险:对隔离 tab 默认开 focus 仿真,`document.hasFocus()` 恒 true 的行为变化。
  缓解:仅在隔离窗口 tab 且随 debugger detach 复位;user 模式不受影响;文档明示
  该仿真边界。
- 风险:`setWebLifecycleState("active")` 每次导航重放,可能让后台 tab 不再被冻结
  (轻微耗电/CPU)。
  缓解:这是唤醒本意(隔离窗口"像可见窗口"工作);会话结束 detach 后自然恢复节流。
- 风险:frameNavigated 重放若对子 frame 误触。
  缓解:仅主 frame(`parentId` 为空)才重放。
- 风险:唤醒命令失败被静默吞掉,排查困难。
  缓解:attach 结果透出 per-command 状态,便于真机观测。

## 里程碑

1. 调研与方案收敛(已完成:多轮真机 + 仓库 history 交叉验证)。
2. 扩展侧实现 + 单测(attach 唤醒 / frameNavigated 重放 / detach 清理 /
   isolated 与 user 模式对照)。
3. 文档(SKILL/ARCHITECTURE/SECURITY)+ 全量验证(`make ci` 相关子集)。
4. 真机冒烟(focus+lifecycle 单独对 x.com 等可见性门控站点是否足够)——需用户
   同意 reload 扩展,验证结果决定是否扩展 session_inject 或调整默认策略。
5. history 记录。

## 验证方式

- 命令:`go test ./...`、`node --test apps/chrome-extension/*.test.mjs`、
  `python3 cmd/open-browser-use/script_helpers_test.py`。
- 新增单测:`background-wake.test.mjs`
  - attach 隔离 tab → fake sendCommand 录制 focus + lifecycle 两条命令;
  - user 模式 attach → 不发送唤醒命令;
  - frameNavigated(主 frame)在已唤醒 tab 上重放两条命令;子 frame 不重放;
  - detach 后集合清理、下次 attach 再唤醒;
  - 唤醒命令失败不影响 attach 主流程(逐命令 catch)。
- 手工检查(真 Chrome,`make local-install` + reload 扩展,用户同意后):
  - 隔离会话 open_tab(x.com) → 首次 evaluate(`document.hasFocus()`、
    `document.visibilityState`、rAF 频率) → 滚动看文章数是否增长;
  - 导航后懒加载仍工作(frameNavigated 重放);
  - user 模式对照:不唤醒、行为不变;finalize 后无残留窗口/tab。

## 进度记录

- [x] 确认范围和约束(用户拍板:B 方案,本轮不做 DOM 注入扩展)。
- [x] 扩展侧实现 + 单测(`background-wake.test.mjs` 6 用例全绿,
      `node --test apps/chrome-extension/*.test.mjs` 8 文件通过)。
- [x] 文档同步(SKILL/ARCHITECTURE/SECURITY)。
- [ ] `make ci` 全量验证。
- [ ] 真机冒烟(需用户同意 reload 扩展)。
- [x] history 记录(随提交)。

## 决策记录

- 2026-08-27:内置程度选 B——隔离窗口 CDP 层自动唤醒(focus + lifecycle),DOM
  注入保持显式(`session_inject`)。用户明确:先不扩展 DOM 注入,要真机验证
  focus + lifecycle 单独是否能让 tab 保持唤醒。
- 2026-08-27:唤醒由"会话有归属窗口(isolated)"触发,而非持久 flag——attach 时
  查 `sessionState.windowId`,免去额外的会话状态字段与同步路径。
- 2026-08-27:维护集合 `wokenTabIds`(tabId 维度)解决 frameNavigated 只有
  tabId、无 sessionId 的反查问题;attach 时加入、detach 时清除。
- 2026-08-27:常驻 attach 否定——createTab/claim 不提前 attach,保持现有
  lazy-attach 架构与"不残留调试横幅"不变量;唤醒在首次 CDP 交互的 attach 路径
  上完成。