# 质量评分

用这份文档按产品区域和架构层次记录当前质量水位，方便持续知道最薄弱的地方在哪。

## 建议的评分标准

- `A`：覆盖完整、行为稳定、文档清楚、运行风险低。
- `B`：整体可接受，但还有明确短板。
- `C`：能用，但需要针对性补强。
- `D`：脆弱、缺少规范，或很多行为尚未定义。

## 当前评分

| 区域 | 评分 | 原因 | 下一步 |
| --- | --- | --- | --- |
| 产品面 | B | Chrome extension、Go native host/CLI、file chooser、Chrome Web Store 发布链路和真实 Chrome smoke 记录已成形；JS/Python/Go SDK 与 MCP server 源码保留但不再对外发布。 | 补首轮用户安装说明和更多失败场景验收。 |
| 架构文档 | B | Chrome route completed plan、架构、安全、发布和 reference 文档已覆盖主要边界；RELIABILITY 新增脚本/事件/任务隔离契约。 | 已有独立 tab ownership 契约；继续补跨层错误恢复与异常退出回收边界。 |
| 插件 UI | B | MV3 popup、icons、content cursor 和基础打包校验已具备。 | 补 popup 截图 smoke 和权限状态展示验收。 |
| 测试 | B | `make ci` 覆盖 docs/repo hygiene、action pinning、extension 打包、脚本语法、Go 测试、JS/Python/Go SDK 协议测试、Python SDK smoke 和 fake native host/extension peer relay 测试；2026-09 复审将原未被 CI 执行的 Python helper 与新增 transport 回归共 60 项纳入门禁。 | 新增 ownership 并发/释放/隔离窗口回归及专用 smoke 脚本；需重载扩展后真实 Chrome 验收，继续补 popup smoke。 |
| 可观测性 | C | native host 和 extension 已有基础错误传播；Python wait 提供脱去 URL 凭据/query/fragment 的事件诊断，但跨层关联日志仍不足。 | 增加 request id、事件分层日志和可脱敏 debug trace。 |
| 安全 | C | Chrome route 明确不内置上层站点策略，manifest origin、0600 socket 和任务路径/实例隔离已有默认约束；尚非同用户不可信进程的安全边界。 | 补 socket token/peer 授权、失败路径安全测试和安装权限审计说明。 |
