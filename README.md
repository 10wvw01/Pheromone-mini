# Pheromone-mini

单节点、单进程内的数据驱动任务调度与 Trigger 预研，V0.6 设计与接口示例统一使用 Go。

当前设计基线：**V0.6**。本模块负责配置初始化、App Registry、DIRECT Dependency、Immediate / ByName Event Trigger、Task Builder 和 Task Router；上游通过两个 API 初始化配置和通知数据事件，下游 Task Coroutine Pool 只提供 TryEnqueue。

## 当前版本入口

- [V0.6 完整软件设计方案](docs/V0.6-完整软件设计方案.md)
- [V0.6 文档导航](docs/README.md)
- [V0.6 总体设计](docs/v0.6/01-总体设计.md)
- [V0.6 核心模块详细设计](docs/v0.6/02-核心模块详细设计.md)
- [V0.6 上下游接口与协作设计](docs/v0.6/03-接口与协作设计.md)
- [V0.6 分功能需求列表](docs/v0.6/06-分功能需求列表.md)
- [V0.6 Mock 端到端与独立验收](docs/v0.6/appendix/E-Mock端到端测试与独立验收.md)
- [V0.6 app.json 示例](docs/v0.6/examples/app.json)
- [V0.6 Go 接口示例](docs/v0.6/examples/task_scheduler.go)

## V0.6 冻结边界

- 实现语言为 Go。
- 上游只调用 TriggerInit(appJSONPath) 和 EventTriggerNotify(event, sourceFuncName, dataAddr, dataSize) 两个 API。
- 全部 TriggerInit 初始化成功结束后才允许 EventTriggerNotify，禁止初始化与事件通知并发。
- TriggerInit 读取用户编写的 app.json；functions[] 内嵌每个 Function 的 so_name、func_name 和完整 Resource（CPU / Memory / GPU / Disk / Network）；Task Router 本期只检查 CPU / Memory。
- DataReadyEvent 仅包含 bucket、key、session_id；数据统一通过 data_addr + data_size 传递，不区分大小数据包，不使用 DataRef。
- Function 信息通过 TaskInvocation 随 TryEnqueue 传给下游；Pool 接口不带 ctx，不提供独立 Function 注册接口。
- Task Router 在 TryEnqueue 前检查 CPU / Memory；资源不足或探测 / 计算失败不调用 TryEnqueue，记录日志后按延时调度配置暂存重试或终止；延时重试也必查资源。
- 数据有效性、Session 任务完成判定和回收由 Data SDK 管控；Pool 实例创建与装配由下游 / 集成方负责，本模块仅调用 API。
- Pool 负责队列消费、协程调度和 Function 加载执行；本模块不规定 Go Plugin、cgo 或 native SO Loader 的具体实现。
- 不实现 Rerun、resource.timeout_ms、wake_one、跨进程和跨节点调度。

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
