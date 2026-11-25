# V0.4 对接示例

app.json 与 function.json 是可联合使用的示例，function.json 为可选单函数补充文件。详见[附录 B](../appendix/B-JSON配置协议示例.md)。

task_scheduler.h、producer.c、processor.c 为 C11 接口与 SO 业务样例；不含 Scheduler、SDK 或协程池实现。编译 SO 及实际调用需要按[附录 C](../appendix/C-C接口与Task-ABI.md)确认部署与接口，不应将本目录当作可独立运行的完整 Demo。
