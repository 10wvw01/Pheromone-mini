# 附录 A：Pheromone 源码映射与 V0.6 裁剪说明

## A.1 参考范围

参考仓库：[MincYu/pheromone](https://github.com/MincYu/pheromone)。

本项目借鉴 Pheromone 的数据驱动执行模型和 Trigger / DIRECT 语义，不复制其完整分布式系统。本轮固定参考提交为 `4d22677aa20dd94069da06d47ec2608504bf6bf4`；已复核 DIRECT 优先分支、Immediate / ByName、输入模式和执行消息相关源码。源码链接固定到该提交；V0.6 的地址传递和 Go API 是本项目裁剪契约，不等同于上游协议。

## A.2 两级调度取舍

| Pheromone | 原职责 | V0.6 |
|---|---|---|
| Coordinator | 选择 Worker Node、维护节点状态和数据位置 | 不实现 |
| Local Scheduler | 选择本地 Executor、处理本地任务调度 | 只保留本地 Task Router 最小提交职责 |
| Warm / Cold Executor | 优先复用已加载 Function 的 Executor | 不实现；下游 Pool 管理执行器 |
| Delay Call Queue | 本地暂存等待资源的 Function Call | 保留为本地 Delay Queue，采用全局配置 |
| Remote Forward | 本地资源不足时转发其他节点 | 不实现，只留日志和扩展点 |

参考源码：
- [func_call_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/func_call_handler.cpp)
- [notify_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/notify_handler.cpp)
- [scheduler_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp)

V0.6 不声称实现 Pheromone 完整两级调度。当前仅覆盖单节点、单进程内的任务准入和延时重试。

## A.3 DIRECT Dependency

Pheromone 的 AppRegistration / DataDependency 使用 DIRECT 类型以及来源函数、目标函数集合。Local Scheduler 在处理 Function 输出对象时先查来源函数是否存在 DIRECT 依赖；存在则调度目标函数，不再进入普通 Bucket Trigger 检查。

参考：
- [operation.proto：DataDependency / AppRegistration](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/proto/operation.proto)
- [app_register_handler.cpp：DIRECT 依赖保留](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/app_register_handler.cpp)
- [scheduler_server.cpp：DIRECT 分支优先于 Trigger 检查](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp)

V0.6 保留 DIRECT 独立路径、来源 Function 到多个目标 Function 的映射，以及 DIRECT 优先于 Bucket Trigger 的规则。每条 DIRECT 配置只声明一个 source func_name，多个目标放入 tgt_functions；DIRECT 的输入模式可由依赖项配置，缺省为 InputValue。固定提交的上游 `DataDependency` 没有 `trigger_option`，本地 DIRECT 分支依据数据打包方式构造参数和 `arg_flag`。0 / 1 / 2 三种输入语义取自上游 Bucket Trigger；在 DIRECT 配置中开放这三项是 V0.6 的设计。

## A.4 Bucket 与 Trigger

Pheromone 的 Trigger 输入核心是 BucketKey（Bucket、Key、Session）；Trigger 根据自身配置产生 TriggerAction。Bucket 到 Trigger 的索引是一个列表，一个 Bucket 可以关联多个 Trigger。

参考：
- [trigger.hpp：BucketKey、TriggerAction、ImmediateTrigger、ByNameTrigger](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/include/trigger.hpp)
- [notify_handler.cpp：遍历 Bucket Trigger 列表](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/notify_handler.cpp)

V0.6 保留 Immediate、ByName、同 Bucket 多 Trigger 独立匹配和 Trigger Option 0 / 1 / 2。

## A.5 Data Bucket 与通知字段

Pheromone 的 KVS 通知包含 BucketKeyAddress、Payload、数据地址和通信控制信息。V0.6 裁剪为：
- DataReadyEvent：bucket、key、session_id；
- source_func_name：独立参数；
- data_addr、data_size：独立参数，统一地址传递。

V0.6 不接收 app_name、active_trigger_names、ips、response_address、request_id、trigger_name、target_function、payload_type 或 DataRef。Bucket 在当前 Registry 全局唯一，本模块自行反查 App。

## A.6 下游执行消息

Pheromone Local Scheduler 向 Executor 发送的本地执行消息核心是函数标识、Session、参数模式和参数内容；Trigger 元数据不传给 Executor。

V0.6 收敛为 TaskInvocation：
- SessionID；
- FunctionInfo：so_name、func_name、resource；
- InputMode；
- Args[]：Key、DataAddr、DataSize。

FunctionInfo 随每个 TaskInvocation 通过 TryEnqueue 传递，不通过单独静态注册 API 交付。Pool 自行消费队列和唤醒协程。

参考：[scheduler_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp)、[executor_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/executor/executor_server.cpp)。

## A.7 本期明确裁剪

- 不实现 Coordinator、Node Scheduler、Process Scheduler。
- 不实现 Warm / Cold Executor 选择。
- 不实现 Rerun、Function 执行超时和独立 wake_one。
- 不实现跨进程、跨节点任务转发。
- 不实现 Data Bucket 存储、共享内存分配、1KB 阈值和底层消息传输。
- 不实现 DataRef、validate、retain、release 和双载体选择。
- 不实现 Pool 静态 Function 注册、Execution 配置或本模块内 SO Loader。
- 不实现 Task Coroutine Pool 内部队列、协程和 Function 执行。
