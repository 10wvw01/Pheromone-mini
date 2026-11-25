# Pheromone-mini

单节点、单进程内的数据驱动任务调度与 Trigger 预研，设计与接口示例使用 C11。

当前设计基线：**V0.6**。本模块负责 App Registry、DIRECT Dependency、Immediate / ByName Event Trigger、Task Builder 和 Task Router；上游提供 DataReadyEvent 与数据引用，下游提供函数注册和原子任务入队能力。

## 当前版本入口

- [V0.6 完整软件设计方案](docs/V0.6-完整软件设计方案.md)
- [V0.6 文档导航](docs/README.md)
- [V0.6 总体设计](docs/v0.6/01-总体设计.md)
- [V0.6 核心模块详细设计](docs/v0.6/02-核心模块详细设计.md)
- [V0.6 上下游接口与协作设计](docs/v0.6/03-接口与协作设计.md)
- [V0.6 分功能需求列表](docs/v0.6/06-分功能需求列表.md)
- [V0.6 app.json 示例](docs/v0.6/examples/app.json)
- [V0.6 C ABI 示例](docs/v0.6/examples/task_scheduler.h)

## V0.6 冻结边界

- 配置只使用一份 app.json；DIRECT 是独立依赖路径，Immediate / ByName 是两种 Bucket Trigger。
- Bucket 在当前 App Registry 管理的全部 App 范围内全局唯一；DataReadyEvent 不传 app_name。
- DataReadyEvent 仅携带 Bucket / Key / Session、DIRECT 所需的可选 source_function，以及 Inline 或 Shared-Memory 数据载体。
- Task Router 通过原子 task_pool_try_enqueue 完成本地资源准入和入队；不调用 wake_one，协程消费和唤醒由 Task Coroutine Pool 内部管理。
- 本地入队无容量时，按 app.json 全局 Delay Queue 配置等待重试；关闭延时调度或等待超时则记录日志。本期不做进程间、节点间分发。
- 不实现 Rerun，不定义 resource.timeout_ms，不实现 Node / Process Scheduler、Data Bucket 底层存储传输或 Task Coroutine Pool。

## 历史版本

- [V0.5 设计基线](docs/V0.5-完整软件设计方案.md)
- [V0.4 历史设计基线](docs/archive/v0.4/V0.4-完整软件设计方案.md)
- [V0.3 历史设计基线](docs/archive/v0.3/V0.3-完整软件设计方案.md)
- [历史版本归档说明](docs/archive/README.md)

当前仓库交付为设计文档与接口示例，不代表软件实现或上下游联调已经完成。

## 仓库管理约定

- main 是唯一长期维护分支。
- docs/*、feature/*、fix/* 等均为短期工作分支；PR 合并后应删除。
- 版本内容以文档和正式 Git Tag 固定，不为每个版本长期保留开发分支。
