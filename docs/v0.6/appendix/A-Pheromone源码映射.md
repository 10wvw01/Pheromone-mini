# 附录 A：Pheromone 源码映射与 V0.6 裁剪说明

## A.1 参考范围

参考仓库：[MincYu/pheromone](https://github.com/MincYu/pheromone)。

本项目借鉴 Pheromone 的数据驱动执行模型和 Trigger / DIRECT 语义，不复制其完整分布式系统。源码路径以 Pheromone main 分支为参考；若后续固定 commit，应在正式实现评审时记录 commit SHA。

## A.2 两级调度取舍

| Pheromone | 原职责 | V0.6 |
|---|---|---|
| Coordinator | 选择 Worker Node、维护节点状态和数据位置 | 不实现 |
| Local Scheduler | 选择本地 Executor、处理本地任务调度 | 只保留本地 Task Router 最小提交职责 |
| Warm / Cold Executor | 优先复用已加载 Function 的 Executor | 不实现；下游 Pool 管理执行器 |
| Delay Call Queue | 本地暂存等待资源的 Function Call | 保留为本地 Delay Queue，采用全局配置 |
| Remote Forward | 本地资源不足时转发其他节点 | 不实现，只留日志和扩展点 |

参考源码：
- [func_call_handler.cpp](https://github.com/MincYu/pheromone/blob/main/src/coordinator/func_call_handler.cpp)
- [notify_handler.cpp](https://github.com/MincYu/pheromone/blob/main/src/coordinator/notify_handler.cpp)
- [scheduler_server.cpp](https://github.com/MincYu/pheromone/blob/main/src/scheduler/scheduler_server.cpp)

V0.6 不声称实现 Pheromone 完整两级调度。当前仅覆盖单节点、单进程内的任务准入和延时重试。

## A.3 DIRECT Dependency

Pheromone 的 AppRegistration / DataDependency 使用 DIRECT 类型以及来源函数、目标函数集合。Local Scheduler 在处理 Function 输出对象时先查来源函数是否存在 DIRECT 依赖；存在则调度目标函数，不再进入普通 Bucket Trigger 检查。

参考：
- [operation.proto：DataDependency / AppRegistration](https://github.com/MincYu/pheromone/blob/main/common/proto/operation.proto)
- [app_register_handler.cpp：DIRECT 依赖保留](https://github.com/MincYu/pheromone/blob/main/src/coordinator/app_register_handler.cpp)
- [scheduler_server.cpp：DIRECT 分支优先于 Trigger 检查](https://github.com/MincYu/pheromone/blob/main/src/scheduler/scheduler_server.cpp)

V0.6 保留：
- DIRECT 是独立依赖路径，不是第三种 Trigger Primitive。
- 来源 Function 到一个或多个目标 Function 的静态映射。
- 来源 Function 有 DIRECT 依赖时优先 DIRECT；本次事件不再执行 Bucket Trigger。
- DIRECT 目标按配置顺序生成独立 Task。

V0.6 简化：
- 只支持 DIRECT；不支持 MANY_TO_ONE、K_OUT_OF_N、PERIODIC 等依赖类型。
- 每条 DIRECT 配置建议只声明一个 source function，多个目标放入 tgt_functions。
- DIRECT 目标默认接收本次提交对象，输入模式为 INPUT_VALUE。
- 不实现远程目标选择和跨节点转发。

## A.4 Bucket 与 Trigger

Pheromone 的 Trigger 输入核心是 BucketKey（Bucket、Key、Session）；Trigger 根据自身配置产生 TriggerAction（目标函数和对应 Session / Key）。Bucket 到 Trigger 的索引是一个列表，一个 Bucket 可以关联多个 Trigger。

参考：
- [trigger.hpp：BucketKey、TriggerAction、ImmediateTrigger、ByNameTrigger](https://github.com/MincYu/pheromone/blob/main/common/include/trigger.hpp)
- [notify_handler.cpp：遍历 Bucket Trigger 列表](https://github.com/MincYu/pheromone/blob/main/src/coordinator/notify_handler.cpp)

V0.6 保留：
- Immediate：有效对象到达即命中。
- ByName：Key 完全相等才命中。
- 同 Bucket 多 Trigger 独立匹配，不按目标函数去重。
- Trigger Option 0 / 1 / 2 对应 Value / Key+Value / Key Only。

V0.6 不实现 BySet、ByBatchSize、ByTime、DynamicGroup、Redundant 等 Primitive。

## A.5 Data Bucket 与通知字段

Pheromone 的 KVS 通知包含 BucketKeyAddress、Payload、数据地址和通信控制信息。Coordinator 还会维护 Bucket 到 App 的映射，并通过 Trigger 列表生成 FunctionCall。

参考：
- [kvs.proto：KeyNotifyRequest](https://github.com/MincYu/pheromone/blob/main/common/proto/kvs.proto)
- [common.proto：BucketKeyAddress](https://github.com/MincYu/pheromone/blob/main/common/proto/common.proto)
- [notify_handler.cpp](https://github.com/MincYu/pheromone/blob/main/src/coordinator/notify_handler.cpp)

V0.6 只要求 Data SDK 提供：
- bucket、key、session_id；
- 函数输出场景的 source_function；
- payload_type、data_size；
- Inline 数据或不透明 DataRef。

V0.6 不接收 app_name、active_trigger_names、ips、response_address、request_id、trigger_name 或 target_function。Bucket 在当前 Registry 全局唯一，本模块自行反查 App。

## A.6 下游执行消息

Pheromone Local Scheduler 向 Executor 发送的本地执行消息核心是函数名、Session、参数模式和参数内容；Trigger 元数据不传给 Executor。

V0.6 将此思想收敛为 TaskInvocation：
- so_name
- session_id
- input_mode
- 可选 key
- payload_type / data_size / Inline 或 DataRef

函数级 resource / execution 只在注册阶段一次性传给 Pool，不在每个 Task 重复传递。Pool 自行消费队列和唤醒协程。

参考：[scheduler_server.cpp](https://github.com/MincYu/pheromone/blob/main/src/scheduler/scheduler_server.cpp)、[executor_server.cpp](https://github.com/MincYu/pheromone/blob/main/src/executor/executor_server.cpp)。

## A.7 本期明确裁剪

- 不实现 Coordinator、Node Scheduler、Process Scheduler。
- 不实现 Warm / Cold Executor 选择。
- 不实现 Rerun 和 Function 执行超时。
- 不实现独立 wake_one 调用。
- 不实现跨进程、跨节点任务转发。
- 不实现 Data Bucket 存储、共享内存分配、1KB 阈值和底层消息传输。
- 不实现 Task Coroutine Pool 内部队列、协程和 SO Loader。
