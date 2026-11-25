# Pheromone-mini

Pheromone-mini 是面向预研 Demo 的轻量级数据驱动任务调度与触发设计。当前阶段聚焦于复用 Pheromone 的核心思想：数据写入驱动任务触发、Data Bucket 组织数据、分层调度将任务送入本地执行队列。

## 设计文档

**主文档：** [V0.3 完整软件设计方案](docs/V0.3-完整软件设计方案.md)

- [文档导航](docs/README.md)
- [第一部分：总体设计（第 1～3 章）](docs/01-总体设计.md)
- [第二部分：核心模块详细设计（第 4～10 章）](docs/02-核心模块详细设计.md)
- [第三部分：接口与协作设计（第 11～14 章）](docs/03-接口与协作设计.md)
- [第四部分：工程实现设计（第 15～18 章）](docs/04-工程实现设计.md)
- [第五部分：需求追踪与测试验收（第 19～21 章）](docs/05-需求追踪与测试验收.md)
- [附录 A：Pheromone 源码映射与裁剪说明](docs/appendix/A-Pheromone源码映射.md)
- [附录 B：JSON 配置协议示例](docs/appendix/B-JSON配置协议示例.md)
- [附录 C：C 语言接口与 Task ABI](docs/appendix/C-C接口与Task-ABI.md)
- [附录 D：数据结构、状态与错误码](docs/appendix/D-数据结构与错误码.md)

## 当前设计基线

- 版本：V0.3
- 设计语言：C11；对外提供稳定 C ABI。
- 调度层级：节点级（全局）、进程级、线程/协程级。
- 触发能力：本期仅实现 ImmediateTrigger。
- 依赖能力：保留 DIRECT 显式依赖分发；DIRECT 与 Bucket Trigger 是两条不同路径。
- 数据边界：Data Bucket SDK 负责共享内存池、对象索引和 DataReady 消息队列；Scheduler 只持有 DataRef，不复制业务数据。
- 执行边界：Task Coroutine Pool 负责任务队列、协程唤醒、SO 加载和 Handler 执行；Scheduler 负责构建和投递 TaskDescriptor。

本仓库当前提交以设计文档为主，接口中的待协商项以“待接口冻结”标识，不代表上下游已经实现。
