# Pheromone-mini

单节点、单进程内的数据驱动任务调度与 Trigger 预研，使用 Go 实现。

正式版本为 **v0.6**。本模块负责配置初始化、App Registry、DIRECT Dependency、Immediate / ByName Event Trigger、Task Builder 和 Task Router；上游通过两个 API 初始化配置和通知数据事件，下游 Task Coroutine Pool 只提供 TryEnqueue。

## 设计与示例

- [正式设计文档](docs/DESIGN.md)
- [可运行的 E2E 示例与接口说明](examples/README.md)

## 功能范围

- 实现语言为 Go。
- 上游只调用 TriggerInit(appJSONPath) 和 EventTriggerNotify(event, source) 两个 API。
- 每份 app.json 配置一个 App。启动时按文件逐份调用 TriggerInit，所有 App 注册成功后再发送通知。
- TriggerInit 读取用户编写的 app.json；functions[] 内嵌每个 Function 的 so_name、func_name 和完整 Resource（CPU / Memory / GPU / Disk / Network）；Task Router 必查 CPU / Memory，按需检查 GPU / Disk / Network。
- SDK 按 app_name + bucket + key 绑定一批数据，整批提交后调用 EventTriggerNotify。事件包含 AppName、Bucket、Key、SessionID 和指向整批数据入口的 DataAddr。
- Bucket 规则直接取自各 App 的 triggers[].bucket。不同 App 可使用同名 Bucket。没有匹配规则时，有效通知成功返回且不生成任务。旧配置请移除顶层 buckets 和 trigger_option。
- 函数来源标识包含 AppName、SOName、FuncName。Pool 的 TryEnqueue 按值接收 AppName、FuncName、SOName、DataAddr、SessionID，Router 在内部使用函数资源配置。
- Task Router 在 TryEnqueue 前检查 CPU / Memory；资源不足或探测 / 计算失败不调用 TryEnqueue，记录日志后按延时调度配置暂存重试或终止；延时重试也必查资源。
- 数据有效性、Session 任务完成判定和回收由 Data SDK 管控；Pool 实例创建与装配由下游 / 集成方负责，本模块仅调用 API。
- Pool 负责队列消费、协程调度和 Function 加载执行；本模块不规定 Go Plugin、cgo 或 native SO Loader 的具体实现。
- 不实现 Rerun、resource.timeout_ms、wake_one、跨进程和跨节点调度。

## 构建和接入

在 Linux 使用 Go 1.26.2 执行：

```sh
go build ./...
go test -count=1 ./...
go vet ./...
go test -race -count=1 ./...
```

上游从 `triggerEngine/api` 导入两项 API 和公共类型。集成方在本模块的部署入口用 `internal/engine.New` 组装真实 TaskPool，通过 `internal/wiring.Install` 在首次 API 调用前安装进程实例。生产资源适配器默认读取 Linux 资源接口。需要 GPU 准入的部署通过 `internal/resource.NewLinux` 提供进程可见设备遥测。关闭时先停止通知源，再调用内部运行时的 `Close`，由集成方管理 Pool 和 SDK 的后续收尾。

测试代码、辅助包和测试数据统一放在 `tests/` 目录。全量测试包含 100 个复杂 `app.json` 配置路由场景（`TestComplexConfigRouting`）和 100 个复杂流程场景（`TestComplexDataDriven`）。
