# V0.6 Go 对接示例

本目录包含 Go 接口 / Task 类型示例、app.json 和按 Function 拆分的 function.json。

- app.json：App 元信息、Function 配置文件引用、Bucket、DIRECT、Trigger 和全局 Delay Queue。
- functions/*/function.json：单 Function 的 so_name、func_name、version 和 CPU / Memory Resource。
- task_scheduler.go：上游两个 API 的 Go 类型契约，以及下游 TryEnqueue 契约。
- producer.go：通过 Object API 写入普通 Bucket；Commit 成功后由 SDK 通知本模块。
- direct_source.go：通过 Object API 写入数据，SDK 从 TaskContext 注入 sourceFuncName，触发 DIRECT 依赖。
- processor.go：消费 Immediate / ByName 输入。
- direct_processor.go：消费 DIRECT 输入。

Go 示例只展示数据处理和调度接口契约，不定义下游 Pool 的动态库加载方式。示例不实现 Data SDK、Task Coroutine Pool、共享内存池或协程调度。
