# 技术债追踪

这里记录那些暂时不阻塞当前任务、但已经值得留档的技术债。

| 日期 | 区域 | 债务描述 | 为什么会存在 | 计划中的后续动作 |
| --- | --- | --- | --- | --- |
| 2026-09-08 | Host relay / P1 | `internal/host/relay.go` 的 broadcast 同步逐客户端写，无 write deadline；慢消费者可能拖住 extension reader 与其他客户端响应。 | 当前简单同步 relay 未定义 backpressure 契约。 | 用阻塞 fake client 复现；为每客户端有界发送队列或可配置写 deadline 评估取舍，不粗暴丢弃有 id 的响应。 |
| 2026-09-08 | Extension / P1 | session 生命周期、分组、CDP、下载集中在 `background.js`；不同测试复制 Chrome fake，难覆盖真实 MV3 重启及并发 create/claim/finalize。 | 功能快速增长但入口仍是单文件。 | 先共用 fake 与生命周期契约测试、加真实 Chrome smoke；不先做大规模模块化。 |
| 2026-09-08 | 传输观测 / P1 | 缺跨 CLI → host → extension 的低噪声关联诊断；active registry 并发写/删除仍值得专门验证。 | 日志仅有基础错误；无统一 redaction/trace 预算。 | opt-in request id/method/duration/status，不默认记录 params/网页正文；测试并发 registry，再决定原子写方案。 |
| 2026-09-08 | Event queue / P2 | Python 尾队列仅 500 帧、进程级；过量事件的 eviction 没有计数指标。默认跨 tab 广播需 predicate 筛选。 | 同步标准库 helper 不承担 durable subscriber 的职责。 | 用实际事件吞吐评估可配置容量/丢弃计数；若需订阅另做可选层，不自动限制 raw event/CDP。 |
| 2026-09-08 | 配置与 memory / P2 | `.obu.env` 任意键可覆盖内部 OBU transport env；空 selector 的文件/继承优先级待补；memory_put 只校验 pipe，换行与 domain 归一化尚未共享同一契约。 | 保留灵活配置与最小 append-only memory 的早期实现。 | 明确内部保留键和空值语义，保留自定义脚本 env；补 memory 行格式/并发/控制字符测试，避免引入新的 memory 数据库。 |
| 2026-09-08 | 文档流程 / P2 | active 下多个全部勾选完成的旧 plan；profile plan 仍有过期 SDK/registry 验收形状。 | 决策追加后没及时归档，设计初稿与最终实现混杂。 | 专门核对并归档完成项、标注被替代的结论；本轮不把未知验收项假标为完成。 |
