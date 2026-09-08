# 保留灵活性的 OBU 运行时复审

## 目标

不增加 DOM DSL、不限制原始 CDP / JSON-RPC、不替代完整 Python；修复任务脚本、事件等待和任务隔离中可复现的基础问题，并把已有测试真正纳入 CI。

## 范围

- 包含：Python 事件保留与传输超时、exec 文件语义、taskinit 路径/实例/selector、回归测试、文档同步。
- 不包含：新 SDK 发布、snapshot/ref 引擎、站点策略、自动重试有副作用操作、Chrome 安装/重载、全局 OBU 替换。
- 开发阶段使用独立 worktree / 分支；完成后按用户要求提交并合并 main，不触碰其他工作区。

## 背景与证据

- 已阅读架构、协作、安全、history、质量评分与已抛弃高效 DOM 层的 A/B history。
- 经用户确认，只读最近 30 天 pi session 中 OBU 调用模式；不落盘原始对话、个人路径、网页数据或私密 URL。
- 启发式筛选约 78 个含匹配调用的 session，exec 为主要入口。记录中有事件 predicate 超时、响应体获取失败、无当前 tab、helper 参数误用；这些混合历史版本和开发实验，不作为当前版本故障率，也不把所有错误归因于 OBU。
- 代码/合成复现确认：等待 responseReceived 会丢弃队列里的 loadingFinished；predicate 异常不恢复 timeout；recv 中途超时丢失半帧。
- exec runner 使用自身 globals 执行文件，导致 __file__ 指向临时 runner、同目录 import 不成立；CLI 不接受脚本参数。
- taskinit 仅校验 slug、不校验 domain；时间戳精度为秒且直接 MkdirAll；跨域同 slug 同秒 session id 相同；partial profile selector 被忽略。
- 基线 make ci 通过；单独 Python helper 测试通过，但 CI 没有运行这个测试文件。

## 风险与约束

- 非匹配事件保留仍使用有界队列（500），不声称持久订阅或无限保留；允许 predicate 自行跨 tab 筛选。
- 半帧缓存与绝对 deadline 必须覆盖队列、socket 读写、异常恢复；超时不会自动重放浏览器操作。
- exec 保持 helper 注入、真实 traceback、cwd 和退出码；以标准库文件执行机制修复语义。
- taskinit 不做严格公网 DNS allowlist，保留 localhost/内网站点/Unicode 域名；仅拒绝破坏目录或 env 布局的组件。
- 明确区分 mock/离线回归与真实 Chrome 验证，本轮不操作用户网页。

## 里程碑 / 进度

- [x] 用户确认工作区与 session 范围；独立 worktree；make ci 基线通过。
- [x] 历史与代码审视，确定小切片和不做项。
- [x] 先补回归复现：事件/传输、exec、taskinit（初始回归确认失败；旧传输 wait 会一直等，测试执行设外层上限）。
- [x] 实现修复，Python helper/transport 共 60 项接入 CI。
- [x] 同步 RELIABILITY、skill、架构/安全事实、待发布记录与分优先级技术债。
- [x] make ci、60 项 Python strict ResourceWarning 测试、Go race、pnpm typecheck、selector/并发任务用例重复 20 次通过；history 已记录。

## 交付结果

- 实现提交 `14f44ab` 已按用户要求快进合并到 main；本轮 review worktree / 分支在收尾时清理，另一既有 worktree 保留。
- 未推送远端、未发布、未执行全局安装；本地使用仍需构建并确认 PATH 指向新版 CLI。
- 未操作真实 Chrome 页面。真机 MV3/多 target/下载抓取 smoke 保留为后续验收，不把 fake socket 测试称为真实浏览器验证。
- 本轮固定 3 个边界：同步 transport、Python 文件执行、任务隔离；没有新增产品层 helper 或外部依赖。
- 已完成但未归档的旧 plan 和历史初稿漂移单独登记为技术债；不假定其中未知验收项已完成。

## 验证方式

- python3 cmd/open-browser-use/script_helpers_test.py（假 host 与 socketpair）
- go test ./cmd/open-browser-use（文件执行、selector 纯函数、并发任务创建）
- make ci；go test -race ./...
- git diff --check；确认 main 干净、没有原始 session 内容落库。

## 决策记录

- 2026-09-08：只修基础设施契约，保留 raw escape hatch；不删除内部 SDK/MCP 和历史 run/heredoc 入口。
- 2026-09-08：先用合成 fixtures 固定数据丢失/隔离问题，不以历史对话推断所有失败都来自当前代码。
- 2026-09-08（用户复审后）：任务目录简化为 `<domain>/<slug>-<unique>/`，保留 MkdirTemp 原子分配及碰撞重试，去掉已不负责唯一性的时间戳；session 仍含 domain，旧任务不迁移。已完成命名/旧任务续跑/mtime 审计回归及文档同步；专项测试重复 20 次、make ci、go test -race ./...、git diff --check 均通过。
