# OBU 实战体验报告评估与优化计划

## 状态

原始候选评估，保留供追溯。生命周期方案已在后续用户评审中替换为
[Tab ownership 分离执行计划](tab-ownership-separation.md)：显式 release、
独立 deliverable 展示，旧 finalize 弃用兼容。下文保留初步判断，不再作为
生命周期实现规格。原评估轮仅核对代码与测试，未访问真实管理后台。原始报告包含私人站点和本机信息，不复制进仓库。

## 目标

降低真实登录态后台任务中的恢复成本、表单误操作和验证盲区，同时保留显式任务隔离、Python 文件审计、evaluate/raw CDP 逃生舱。优先修复可证实的契约矛盾，不按单次体验扩建通用浏览器操作系统。

## 证据与判断

- `apps/chrome-extension/background.js` 的 `finalizeTabs` 刻意保留 handoff 的 session、tab origin 和 active tab；detach debugger 不等于 release ownership。
- `skills/open-browser-use/references/tabs.md` 定义 handoff 为同一任务下一轮续跑，但其 Reuse Before Opening 又建议直接 claim earlier handoff group；不同 session 下会冲突。
- `claimUserTab` 的冲突信息笼统建议原 owner finalize，未说明 handoff finalize 不释放。
- deliverable 会移到共享成果组并从原任务 tabOrigins 移除；报告提出的“deliverable 仍保有 ownership”并非当前契约。
- `taskinit.go` 的唯一目录是隔离设计，`taskinit_isolation_test.go` 明确覆盖同名并发任务不得共享目录和 session。不能按 domain/slug/时间接近自动复用。
- 报告中的 token 输入问题可以说明输入文本不等于提交业务值，但没有原始 trace，无法断言所有失败均来自 controlled input setter。
- 401 challenge 和 metadata 200 是有价值的独立观察，不足以证明 OAuth 客户端端到端可用或所有源站入口均受保护。

## 范围与边界

包含任务恢复可发现性、生命周期文档与结构化诊断、有限的语义交互试点、分层验证、有限 memory 上下文。

不包含自动抢占/合并 session、静默切 profile、通用框架内部状态探测、自动重复提交、完整授权引擎、自动运行 memory 中脚本、生产账户写入型 CI。

## 阶段 1：契约、指引和最小回归（P1，先做）

- 统一 skill、tabs reference、Python docstring/api-sheet 与架构文档：handoff 是同一 session 暂停；deliverable 是交给用户的成果；用户原有 tab 默认保留并释放，agent 临时 tab 默认关闭。
- 修正“旧 handoff 直接 claim”和 ownership 冲突恢复建议。明确从原任务脚本目录 exec 恢复，不再 taskinit；不要只切 session 而遗失 target 与 workspace。
- Safety 文案明确已有、具体、仍有效的用户授权可覆盖范围内提交；越界才重新询问。区分上层确认要求、真实工具错误与不可覆盖限制。
- 增加输入文本/提交 token/持久化保存三个不同后置条件的 technique；不默认 Enter，避免触发表单提交。
- 将 UI mutation → independent verification 提升为主流程要求（存在适当只读外部观察方式时）；说明验证范围与未验证部分。
- 回归覆盖同 session 续跑、异 session claim 冲突、deliverable 再 claim、用户 tab 不误关。

验收：指引不再把 agent 引向无效 finalize 或重复 taskinit；既有 handoff 行为不变。

## 阶段 2：显式恢复与诊断（P1）

- 提供可按 domain/target 限定的任务查询、任务详情和显式恢复入口。优先基于任务目录恢复 `.obu.env`；具体 CLI 命名在实现评审时确定。
- 输出 workspace、session、准确 browser/profile、观测到的 tab 状态；本地任务记录和实时 browser ownership 分开显示，离线状态必须标为 unknown。
- claim 冲突携带稳定 error code 和结构化 owner 信息；CLI/Python 保留结构，不要求解析英文错误。workspace 映射由 CLI 本地解析，不放进 extension。
- taskinit 可在有明确关联证据时输出续跑候选，但仍创建独立实例，不按相似 slug/域名/近期活动合并。
- 原任务目录不存在、target 离线、候选多个、owner 仍工作时不自动接管。
- 新增跨任务 release/transfer 仅在证明 deliverable + 显式恢复不足时推进。须先定义 ownership 与 tab group/window 分离、并发与部分失败语义，不能只删除 tabOrigins。

验收：新的 agent 可通过明确任务身份续跑；同名并发任务仍隔离；错误 profile 不会回退到 active；MV3 重启及隔离窗口场景可恢复或给出准确失败。

## 阶段 3：小型语义层实验（P2，收益验证后推广）

- 建立专用本地 fixture：native input/select、controlled input、可创建 token 的 combobox、重复 label、延迟渲染、保存失败。
- 先实现范围有限的只读页面摘要和严格 locator：role/name、associated label、显式 CSS；多匹配报错，不静默遍历模糊 fallback 后点击。
- 摘要限制大小、可按 dialog/form scope 获取；暴露 frame 与观测范围。引用绑定文档生命周期，导航后失效，不承诺跨重渲染永久稳定。
- 试点区分 fill_text、press、select_option、add_token 与 wait_for；名称仅为候选，不同时推出多套等价 API。
- 动作和后置条件分开，返回实际证据、timeout、unsupported 或 unknown。不得把 DOM value 相等包装成 framework_state_observed=true。
- 写操作不自动重放；超时后先观察是否已生效。键盘和 setter 策略须明确，不依次盲试多个可能产生副作用的路径。
- 优先放嵌入 Python/helper 或可复用 technique；extension 继续负责生命周期与 Chrome primitives。新增依赖前核对对应官方文档。

验收：token 生成后才认为 token 添加成功；保存错误不报告成功；重复 label 不误点；超时不会重复提交。与 primitives 基线比较，失败率不升且总调用/耗时/上下文成本有可测收益，才扩大组件覆盖。

## 阶段 4：上下文与运行证据（P2）

- taskinit/显式恢复输出受限长度的匹配 memory 原始条目、时间、来源路径和关联文件名；大文件仍按需读。
- 采用明确域名匹配规则，不默认把同注册域的不同应用视为同站点；报告中短别名与完整域名不一致的问题应提示或显式映射。
- memory 作为可能过期的参考数据，不作为授权，不自动执行关联 runbook，不无界注入全部 technique。
- 成功 flow 只有在参数化、凭据脱敏、前置条件、验证和重复运行风险评审后才晋升 runbook。
- 记录完成率、错误成功报告数、恢复所需操作、browser RPC、上下文字节/可取得的 token、耗时及重试次数；本地记录不默认上传敏感浏览信息。

## 验证方式

1. 单元/假 Chrome：ownership 生命周期、并发隔离、错误传播与任务恢复。
2. 本地浏览器 fixture：表单 commit、locator 歧义、navigation/stale refs、超时与保存拒绝。
3. 专用 Chrome smoke：user/isolated window、MV3 重启、handoff 同 session 恢复、deliverable 跨 session 接管、不抢焦点与清理。
4. 可选真实 SaaS 验收：专用测试资源、明确变更授权、记录原配置与回滚；不能作为默认 CI。
5. OAuth 类任务分层验收：UI 回读；未认证 HTTP challenge；metadata；真实客户端授权/token/资源调用（需对应授权）。额外源站入口防绕过应单独检查，不能由单一路径 401 推定。

实现交付运行 `make ci`。CLI/Python 改动须 `make local-install` 并验证真实 PATH 入口；extension/host 生命周期改动说明重载要求，再做专用页 smoke。同步相关文档与 history。

## 风险与回滚

- 生命周期兼容性：保留 handoff 默认语义，新增能力采用显式 opt-in；不自动迁移旧 workspace。
- 隐式接管：恢复与跨任务转移分离；发现 owner 不代表获准共享 session。
- 语义层过度承诺：先做 fixture 与可观测后置条件，未支持组件明确失败；可以撤回 helper 而不影响 primitive。
- 外部验证误报：每层分别标记通过、失败、未验证；只读检查也限制目标、超时和敏感输出。

## 进度记录

- [x] 阅读体验报告及当前架构、skill、生命周期、任务隔离代码。
- [x] 定位文档矛盾，确认报告的 handoff 修复建议会改变既有契约。
- [x] 运行既有针对性测试：finalize-detach、isolated-window、任务并发唯一性与 target selector 测试均通过。
- [ ] 评审候选计划并选择首个实现切片。
- [ ] 实现阶段 1 和阶段 2，补新回归。
- [ ] 收集语义层基线，按实验收益决定阶段 3 范围。
- [ ] 完成上下文改进、真实 smoke 和交付。

## 决策记录

- 不采纳 handoff 自动 release 和 taskinit 启发式自动 resume；前者破坏续跑契约，后者破坏并发隔离。
- 先修可发现性和错误建议，再评估新增生命周期 API。
- 不把单次实战评分当量化质量基线，也不保证减少调用必然减少总 token 或耗时。
