# V0.6 Go 对接示例

本目录包含 Go 接口 / Task 类型示例和唯一配置文件 app.json。用户按[附录 B](../appendix/B-JSON配置协议示例.md)自行编写配置；TriggerInit 成功结束后才可发送事件。

- app.json：App 元信息、内嵌 Function（so_name、func_name、version、完整 Resource）、Bucket、DIRECT、Trigger 和全局 Delay Queue。
- task_scheduler.go：上游两个 API 的 Go 类型契约，以及下游 TryEnqueue 契约。
- producer.go：通过 Object API 写入普通 Bucket；Commit 成功后由 SDK 通知本模块。
- direct_source.go：通过 Object API 写入数据，SDK 从 TaskContext 注入 sourceFuncName，触发 DIRECT 依赖。
- processor.go：消费 Immediate / ByName 输入。
- direct_processor.go：消费 DIRECT 输入。

Go 示例只展示数据处理和调度接口契约，不定义下游 Pool 的动态库加载方式。示例不实现 Data SDK、Task Coroutine Pool、共享内存池或协程调度。

端到端测试环境另见[附录 E](../appendix/E-Mock端到端测试与独立验收.md)：本目录接口 / Function 示例不是可执行验收框架，不应据此标记 Mock E2E 已通过。
