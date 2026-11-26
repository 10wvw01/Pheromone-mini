# Pheromone-mini 设计文档导航

**当前唯一有效设计基线：[V0.6 完整软件设计方案](V0.6-完整软件设计方案.md)**

V0.6 使用 Go 实现。上游仅有 TriggerInit 和 EventTriggerNotify 两个 API；配置由 app.json 引用多个单函数 function.json；DIRECT 为独立依赖路径；Bucket Trigger 仅支持 Immediate / ByName；下游 Pool 唯一交互为不带 ctx 的 TryEnqueue(TaskInvocation)。

## V0.6 文档

| 文档 | 内容 |
|---|---|
| [V0.6 完整软件设计方案](V0.6-完整软件设计方案.md) | 唯一现行主文档、版本基线和文档入口 |
| [01-总体设计](v0.6/01-总体设计.md) | 业务链路、研发边界、总体架构 |
| [02-核心模块详细设计](v0.6/02-核心模块详细设计.md) | JSON Parser、Registry、DIRECT、Trigger、Task Builder、Task Router |
| [03-接口与协作设计](v0.6/03-接口与协作设计.md) | 两个上游 API 和下游 TryEnqueue 契约 |
| [04-工程实现设计](v0.6/04-工程实现设计.md) | Go 工程结构、并发、所有权、资源探测和 Delay Queue |
| [05-需求追踪与测试验收](v0.6/05-需求追踪与测试验收.md) | 测试矩阵、验收标准、上下游联调 |
| [06-分功能需求列表](v0.6/06-分功能需求列表.md) | 按模块拆分的 P0 功能需求和责任边界 |

## V0.6 附录与示例

- [附录 A：Pheromone 源码映射与裁剪说明](v0.6/appendix/A-Pheromone源码映射.md)
- [附录 B：app.json / function.json 配置协议](v0.6/appendix/B-JSON配置协议示例.md)
- [附录 C：Go 接口与 Task 契约](v0.6/appendix/C-Go接口与Task契约.md)
- [附录 D：数据结构、所有权与错误码](v0.6/appendix/D-数据结构与错误码.md)
- [app.json 示例](v0.6/examples/app.json)
- [function.json 示例目录](v0.6/examples/functions/)
- [Go API 与 Task 类型示例](v0.6/examples/task_scheduler.go)
- [Go Producer / Processor 示例](v0.6/examples/)

## V0.6 关键协作边界

- 上游 Data Bucket SDK：对象写入与 Commit、提供 bucket/key/session_id 和数据地址，维护 Session 生命周期；Commit 成功后调用 EventTriggerNotify。
- 本模块：TriggerInit、JSON 解析与校验、内部 Registry、DIRECT 匹配、Bucket Trigger 匹配、Task Builder、CPU / Memory 预检查和 Delay Queue。
- 下游 Task Coroutine Pool：只提供 TryEnqueue(TaskInvocation)，自行管理队列、协程调度、SO / Function 加载与执行。
- app.json 保存 App 级 Bucket / DIRECT / Trigger / Delay Queue 配置，并引用单函数 function.json；function.json 保存 so_name、func_name 和 Resource。
- 不实现 DataRef、双数据载体、Pool 静态 Function 注册、Execution 配置、Rerun、跨进程 / 节点调度。

## 历史版本

- [V0.5 设计基线](V0.5-完整软件设计方案.md)
- [V0.4 主文档与配套文件](archive/v0.4/README.md)
- [V0.3 主文档与配套文件](archive/v0.3/README.md)
- [归档规则](archive/README.md)
