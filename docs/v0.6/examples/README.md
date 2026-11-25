# V0.6 C11 对接示例

本目录包含唯一配置文件 app.json，以及 C11 公共 ABI 和无状态 handler 示例。

- app.json：App、Function、DIRECT、Bucket、Trigger 和全局 Delay Queue。
- task_scheduler.h：V0.6 上下游 C ABI 草案。
- producer.c：通过 Object API 写入普通 Bucket。
- direct_source.c：通过 Object API 写入数据，SDK 从 TaskContext 注入 source_function，触发 DIRECT 依赖。
- processor.c：消费 Immediate / ByName 输入。
- direct_processor.c：消费 DIRECT 输入。

所有 SO 固定导出 handler。示例不使用文件 I/O、网络 Socket 或可变全局状态。示例不实现 Data SDK、Task Coroutine Pool、共享内存池或协程调度。
