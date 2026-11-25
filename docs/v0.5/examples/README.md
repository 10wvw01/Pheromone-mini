# V0.5 对接示例

本目录只保留一份 [app.json](app.json)，上游将其完整内容传给 register_app。不存在 function.json。

- [task_scheduler.h](task_scheduler.h)：V0.5 C ABI 草案。
- [producer.c](producer.c)：无状态 handler，读取输入并通过 Object API 写入下一 Bucket。
- [processor.c](processor.c)：无状态 handler，读取输入并通过 Object API 写入结果 Bucket。

两个 SO 均只导出 handler；不使用文件 I/O、网络 Socket 或其他旁路数据交换。示例不包含 Scheduler、Data SDK、共享内存池或 Task Coroutine Pool 的实现，不能单独运行完整业务链路。
