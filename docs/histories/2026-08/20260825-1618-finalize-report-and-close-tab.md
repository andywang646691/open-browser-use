## [2026-08-25 16:18] | Task: finalize_tabs 可验证化 + close_tab 兜底清理

### 🤖 Execution Context

- **Agent ID**: pi-assistant
- **Base Model**: 当前会话模型
- **Runtime**: pi / Node test + Go CLI + python3 heredoc

### 📥 User Query

> 用户贴出一段真实 agent 记录：agent 调 `finalize_tabs()` 无报错就宣称
> 「tab 清理完毕」，被用户质疑后实测发现自己开的 x.com 标签页仍然开着，
> 只好绕道 claim + CDP `Target.closeTarget` 三轮才关掉。
> 问「是否经历了挫折」→ 归因后确认这是产品缺陷（静默失败），用户拍板：
> P1 finalize 返回明细报告；P2 新增 close_tab 兜底；并要求补记 history。

### 🛠 Changes Overview

**Scope:** Chrome extension 后端、heredoc prelude、SKILL.md、新增回归测试。

**Key Actions:**

- `background.js` `finalizeTabs`：返回值从空对象改为明细报告
  `{closed, released, kept}`，分别列出被关闭的 agent 标签页、被释放回用户的
  claimed 标签页、按 status 保留的标签页。当 `getSessionTabs` 返回空（会话
  状态丢失或 tab group 已解散）时附加 `warning`，明确说「nothing was
  closed」并指向 `user_tabs()` 复核 + `close_tab()` 清理。此前这条路径会静默
  降级为空 no-op，agent 只能误信成功。
- `background.js` 新增 `closeUserTab(tabId)`：按 id 关闭任意真实标签页，
  拒绝 `chrome://` 页，未知 id 报错；方法分发是通用的（`backend[method]`），
  Go 层零改动。
- `script_helpers.py`：新增 `close_tab(tab_id)` prelude 助手（复用
  `_coerce_tab_id`，接受数字字符串），docstring 注明定位是「finalize 报
  warning 或有残留 tab 时的清理兜底」；`finalize_tabs` docstring 补充返回值
  契约；`_help()` 列表登记 `close_tab`。
- `SKILL.md`「End a task」同步一句：finalize 返回 `closed/released/kept`，
  出现 `warning` 时用 `user_tabs()` 复核并用 `close_tab(id)` 清理。
- 新增 `finalize-report.test.mjs`，覆盖三个场景：① 正常 finalize 报告精确
  列出 closed/released/kept 且真的关了 tab；② 会话壳丢失 → 返回 warning
  而非静默空报告，且未误碰任何 tab；③ closeUserTab 正常关闭 / 拒绝
  `chrome://` / 未知 id 报错。

### 🧠 Design Intent (Why)

- 沿用本仓库已验证的工具设计模式：**把恢复路径写进错误/返回值里**（前一轮的
  「already part of browser session X」报错带 `OBU_SESSION_ID=X` 提示即同源）。
  Agent 是逐轮读输出的程序，只对眼前消息负责——finalize 静默成功时，「验证」
  这个动作根本不会被触发；把故障变成信号，自愈才可能发生。
- P1 与 P2 是配套的：P1 负责**发现问题**（失败不再沉默），P2 提供**处理手段**
  （省掉 agent 手工 CDP 绕路的三轮成本）。只有其一都不完整。
- 刻意不改 finalize 的底层枚举机制（仍靠 group membership），先让失败可见；
  枚举健壮化（如按 tabOrigins 反查）留作后续观察。

### 📁 Files Modified

- `apps/chrome-extension/background.js`
- `apps/chrome-extension/finalize-report.test.mjs`（新增）
- `cmd/open-browser-use/script_helpers.py`
- `skills/open-browser-use/SKILL.md`

### ✅ Verification

- `node --test apps/chrome-extension/*.test.mjs`：5/5 通过
- `python3 cmd/open-browser-use/script_helpers_test.py`：22/22 通过
- `go test ./...` 全绿；`make check-repo` 通过
