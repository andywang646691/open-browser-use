# Tab ownership 分离执行计划

## 目标与已确认决策

用户确认：workspace 跨轮保留，ownership 正常每轮释放；claim/release/close/
mark_deliverable 各自职责独立。删除 handoff 新用法，不新增 end_turn，finalize
仅暂留弃用兼容。旧 API 不静默改义。自动 lease/force takeover 不在范围内。
本计划覆盖此前体验评估计划中生命周期部分，语义表单层等不在本轮实现。

## 实现与风险

- 显式 tabOrigins 取代分组/窗口推断 ownership，保留旧持久化格式。
- 扩展支持 releaseTabs、markDeliverable；closeUserTab 要求 ownership。
- Chrome async claim 通过串行转移防止双领取；释放失败返回逐 tab 结果。
- Python/CLI/run/API sheet 与 Skill 更新；旧 finalize 弃用提示。
- 风险：旧 close fallback 脚本需先 claim；旧 handoff 残留不自动回收；扩展
  必须重载才支持新 wire 方法。同 session 多执行者仍需上层协调。
- 回滚：保留旧接口；需要整体回滚时同步 CLI/扩展/Skill，原 task env 不迁移。

## 验证与交付

- [x] 隔离 worktree 建立；安装锁定依赖；make ci 基线通过。
- [x] 显式 ownership、新操作、弃用入口及 helper 实现。
- [x] 测试重新 claim、并发 claim、普通/隔离窗口、store 重建、关闭追踪、
  成果不释放、detach/持久化失败保留 ownership。
- [x] 新一轮 make ci 通过（后续小改动需最终再跑）。
- [x] 最终 make ci/diff 检查通过。
- [x] make local-install，更新实际 PATH 指向的主仓库 linked package 二进制；
  api-sheet 与 CLI help 验证新 helper/命令，不只检查版本号。
- [x] 合并 main 并重载扩展后，专用 about:blank 页面真实 Chrome smoke 通过：
  release 不关闭页面、重领、mark 保持 ownership、跨 session 领取分组内已释放页。
  所有测试页均显式关闭，相关 session ownership 释放。
- [x] history 与最终结果同步；真实扩展验收明确未完成。

## 进度记录

2026-09-11：新 Skill 统一 release；旧 finalize 留兼容但退出 API sheet。
初次验收时扩展仍是旧进程；合并 main 后用户重载，新接口及真实 smoke 已验证。
测试 data URL 标题未就绪且等待后仍失败，未确认导航根因；改为直接填充新建
about:blank fixture，排除不相关导航因素后 ownership 流程通过。
