# Pheromone-mini 设计文档导航

**当前唯一有效设计基线：[V0.6 完整软件设计方案](V0.6-完整软件设计方案.md)**

V0.6 采用单一 app.json、DIRECT 独立依赖路径、Immediate / ByName 两种 Bucket Trigger、全局唯一 Bucket 索引、最小 DataReadyEvent、精简 TaskInvocation、原子 try_enqueue 和进程级全局 Delay Queue（多 App 配置必须一致）。不实现 Rerun、单次执行超时、wake_one、跨进程或跨节点调度。

## V0.6 文档

| 文档 | 内容 |
|---|---|
| [V0.6 完整软件设计方案](V0.6-完整软件设计方案.md) | 唯一现行主文档、版本基线和文档入口 |
| [01-总体设计](v0.6/01-总体设计.md) | 功能需求列表概述、研发边界、总体架构 |
| [02-核心模块详细设计](v0.6/02-核心模块详细设计.md) | 核心模块、功能点、C 伪代码和时序图 |
| [03-接口与协作设计](v0.6/03-接口与协作设计.md) | 第一部分：提供给 Data SDK 的接口；第二部分：对 Task Coroutine Pool 的接口要求 |
| [04-工程实现设计](v0.6/04-工程实现设计.md) | C11 工程结构、并发、所有权、Delay Queue 和错误处理 |
| [05-需求追踪与测试验收](v0.6/05-需求追踪与测试验收.md) | 测试矩阵、验收标准、上下游联调 |
| [06-分功能需求列表](v0.6/06-分功能需求列表.md) | 按模块拆分的 P0 功能需求和责任边界 |

## V0.6 附录与示例

- [附录 A：Pheromone 源码映射与裁剪说明](v0.6/appendix/A-Pheromone源码映射.md)
- [附录 B：app.json 配置协议](v0.6/appendix/B-JSON配置协议示例.md)
- [附录 C：C 接口与 Task ABI](v0.6/appendix/C-C接口与Task-ABI.md)
- [附录 D：数据结构、所有权与错误码](v0.6/appendix/D-数据结构与错误码.md)
- [app.json 示例](v0.6/examples/app.json)
- [C ABI 与函数示例](v0.6/examples/)

## V0.6 关键协作边界

- 上游 Data Bucket SDK：对象写入与 Commit、1KB 阈值、Inline / Shared-Memory 载体选择、DataRef 生命周期；通知中不传 app_name 和 Trigger 配置。
- 本模块：App Registry、DIRECT 匹配、Bucket Trigger 匹配、Task Builder、Task Router、Delay Queue。
- 下游 Task Coroutine Pool：静态函数配置注册、原子 try_enqueue、SO 加载和 handler 执行；自行管理队列消费和协程唤醒。
- 本期不实现：Rerun、resource.timeout_ms、wake_one、Node / Process Scheduler、跨进程/节点分发、Data Bucket 底层存储和 Task Coroutine Pool。

## 历史版本

- [V0.5 设计基线](V0.5-完整软件设计方案.md)
- [V0.4 主文档与配套文件](archive/v0.4/README.md)
- [V0.3 主文档与配套文件](archive/v0.3/README.md)
- [归档规则](archive/README.md)
