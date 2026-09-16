# Extension 能力对齐 Chrome DevTools MCP

## 目标

为 Open Browser Use 增加与 `chrome-devtools-mcp` Extensions 类别等价的五项能力：安装 unpacked extension、列出扩展、按 ID reload、触发默认 action、按 ID 卸载；能力通过当前 OBU JSON-RPC/CLI/脚本 SDK 暴露，并复用 Direct CDP 的 `Extensions.*` 域，不改变默认的用户 Chrome 与 tab ownership 模型。

## 范围

- 包含：
  - Direct CDP `Extensions.getExtensions`、`loadUnpacked`、`triggerAction`、`uninstall` 封装。
  - OBU backend RPC、CLI、Python prelude、JS SDK、Go SDK 的 Extension API。
  - trigger action 的精确 `tabId -> targetId` 映射与当前 session 默认 tab 解析。
  - 单元/协议测试、运行文档、security/history 同步。
- 不包含：
  - 恢复或重新公开当前已下线的 `obu mcp` 产品入口。
  - Chrome Web Store 扩展安装、已打包扩展安装、任意 profile 间跨实例控制。
  - 对不可信远程调用者提供新的认证边界；当前 socket 仍是同用户本地强能力通道。

## 背景

- 相关文档：`docs/ARCHITECTURE.md`、`docs/SECURITY.md`、`docs/RELIABILITY.md`、`docs/HISTORY_GUIDE.md`。
- 参考实现：Chrome DevTools MCP 的 `src/tools/extensions.ts` 与 Puppeteer `Browser.installExtension` / `Browser.extensions` / `Extension.triggerAction`。
- CDP 参考：`Extensions.getExtensions`、`Extensions.loadUnpacked`、`Extensions.triggerAction`、`Extensions.uninstall`。
- 相关代码路径：`internal/cdp/`、`internal/host/relay.go`、`apps/chrome-extension/background.js`、`cmd/open-browser-use/`、`packages/open-browser-use-{js,python,go}/`。
- 已知约束：Extension CDP 域是 Browser Root 能力；当前 OBU 默认页面 CDP 使用 extension route，但 Extension 管理操作会独立初始化 Direct Browser Root WebSocket，并要求 Chrome 暴露该能力。安装路径必须是本机可访问的 unpacked extension 目录。

## 风险

- 风险：扩展安装/卸载/触发 action 是高权限、可改变浏览器状态的操作。
  - 缓解：保持本地调用边界；安装与卸载使用显式命令；trigger 只接受当前 session 所拥有的 tab，并通过扩展返回的精确 target mapping，不按 URL/title 猜测。
- 风险：Chrome 版本或启动方式不支持 `Extensions.*`。
  - 缓解：返回明确的 Direct CDP/Chrome 支持错误；不自动重试或静默切换到不等价实现。
- 风险：Direct CDP 初始化可能触发远程调试授权或等待。
  - 缓解：复用现有 single patient handshake、macOS approval helper、caller deadline 和日志链路。
- 风险：reload/uninstall 导致 service worker target 变化。
  - 缓解：高层 API 只报告 Chrome 命令结果；后续目标发现仍由现有 `getInfo`/tab event 机制处理，并覆盖缓存/迟到响应测试。

## 里程碑

1. 调研与方案收敛：确认 CDP 命令形状、路径校验、OBU RPC 返回结构和 trigger tab 选择规则。
2. 分阶段实现：Direct CDP manager → relay RPC → CLI/SDK → 文档和测试。
3. 验证、交付与收尾：运行 Go/JS/Python/extension CI；用专用 Chrome profile 做真实安装、列表、触发、reload、卸载 smoke；归档 plan 并写 history。

## 验证方式

- 命令：`go test ./...`、Python SDK unittest、`node --test apps/chrome-extension/*.test.mjs`、相关 package tests、最终 `make ci`。
- 手工检查：专用 unpacked MV3 fixture；确认安装返回 ID、列表包含完整 metadata、reload 后仍存在、trigger action 可产生 popup/action side effect、uninstall 后消失。
- 观测检查：Direct CDP initialization/approval 日志无重复 Browser Root 连接；trigger 使用精确 target ID；失败时不遗留 session、socket 或临时 tab。

## 进度记录

状态：实现、测试与专用 Chrome smoke 已完成。

- [x] 确认五项 Extension 能力和当前 OBU 边界。
- [x] 完成 CDP/Puppeteer/Chrome DevTools MCP 参考调研。
- [x] 完成 Direct CDP Extension 域封装和 relay RPC。
- [x] 完成 CLI、Python、JS、Go 接口。
- [x] 完成测试、文档、history 和真实 Chrome 验证。

## 决策记录

- 2026-09-16：以 OBU 当前 JSON-RPC/CLI/SDK 为产品入口，不恢复已下线的 `obu mcp`；Extension 能力通过 Direct CDP Browser Root 实现。
- 2026-09-16：`triggerExtensionAction` 接受可选 `tabId`；省略时只使用当前 session 的唯一逻辑 active tab，禁止跨 profile 或按 URL/title 猜测。由于 Chrome 要求 `Extensions.triggerAction` 使用 synthetic `type=tab` target，Host 在启用 Target discovery 后按精确 page target 身份解析对应 tab target，再发送 action。
- 2026-09-16：只支持 unpacked extension 生命周期，与 Chrome DevTools MCP 的 `Extensions.*` CDP 语义保持一致；不伪造对 Web Store 扩展的卸载支持。
