# 附录 A：Pheromone 源码映射与 V0.5 裁剪说明

参考项目：[MincYu/pheromone](https://github.com/MincYu/pheromone)，源码研究基准固定为 commit `4d22677aa20dd94069da06d47ec2608504bf6bf4`。原项目能力用于解释设计来源，不代表本 Demo 全部实现。

## A.1 源码索引与本项目取舍

| 原项目能力 | 代表源码 | V0.5 处理 |
|---|---|---|
| Coordinator / Local Scheduler 两级调度 | README、coordinator、scheduler | 只借鉴数据驱动和本地优先思想；不实现节点/进程调度 |
| 全局 Worker 选择 | func_call_handler.cpp | 不实现 |
| 数据位置感知与全局 Trigger | notify_handler.cpp | 不实现 |
| 本地冷热 Executor 选择、延迟队列和上送 | scheduler_server.cpp | 不实现；本地提交下游唯一 TaskQueue |
| App 注册 | app_register_handler.cpp、operation.proto | 收敛为上游传入单份 app.json 调用 register_app |
| Function 元信息 | operation.proto、AppRegistration | 放入 app.json 的 functions[]，由 App Registry 内部 Function Index 管理 |
| Bucket / Key / Session | common.hpp、function_lib.hpp | 保留逻辑概念；底层对象与共享内存由 Data SDK 管理 |
| Trigger 注册 | trigger_op_handler.cpp、operation.proto | app.json 静态声明，构建内部 Trigger Index |
| Immediate | trigger.hpp | P0 实现，每个有效事件命中 |
| ByName | trigger.hpp | P0 实现，Key 完全相等时命中 |
| ByBatchSize / BySet / DynamicGroup / Redundant / ByTime | trigger.hpp | 不实现 |
| DIRECT Dependency | operation.proto、comm_helper.hpp、scheduler_server.cpp | 原项目存在；V0.5 明确不实现，不保留独立分支 |
| TriggerAction / 参数还原 | object_handlers.cpp | 收敛为 TaskAction → TaskDescriptor |
| Object 创建、共享内存和通知 | function_lib.hpp | Data Bucket SDK 负责；本模块只接收事件载体 |
| SO 加载与执行 | executor_server.cpp、cpp_function.hpp | 下游 Pool 负责；本项目固定 handler 入口 |

## A.2 两级调度的源码思想

Pheromone Coordinator 负责跨节点 Worker 选择，Local Scheduler 负责本地 Executor 选择与调度。本 Demo 的范围只有一个节点、一个进程和一个下游 Task Coroutine Pool，因此不需要 Coordinator、节点资源表、进程资源表、冷热实例、远程转发或数据位置感知。

本项目 Task Router 不是 Pheromone Local Scheduler 的完整复刻：它只调用下游 try_enqueue，并在成功后调用 wake_one。队列满时只打印 PROCESS_DISPATCH_REQUIRED，不实际跨进程分发。

## A.3 Data Bucket 与数据载体

保留 Bucket、Key、Session 的逻辑命名方式，但不复制 Pheromone 的共享内存命名拼接、IPC 或对象回收实现。

V0.5 根据 Data SDK 显式提供的 payload_type 处理数据：

- INLINE：事件消息携带小数据，Task Builder 复制到 TaskDescriptor。
- SHARED_MEMORY：事件携带 DataRef，Task Builder retain 后传递引用。
- 1KB 阈值及载体选择属于 Data SDK；Scheduler 不重复判断。
- 同 Bucket/Key 覆盖但没有新通知，不会触发新的 Trigger。
- DataRef 必须标识本次提交的不可变版本；覆盖写不能改变已 retain 引用指向的数据。

## A.4 Trigger 能力映射

| Pheromone Primitive | 原项目语义 | V0.5 |
|---|---|---|
| Immediate | 每个对象到达产生 Action | P0 |
| ByName | Key 与指定名称匹配时产生 Action | P0 |
| ByBatchSize | 按对象数量累计 | 不做 |
| BySet | 等待指定 Key 集合 | 不做 |
| DynamicGroup | 按 Key 前缀分组 | 不做 |
| Redundant | K/N 冗余触发 | 不做 |
| ByTime | 到达时检查时间条件 | 不做 |
| DIRECT | 源函数显式依赖目标 | 不做 |

Immediate / ByName 都是无状态匹配，不维护历史对象、计数器或 Session 聚合。一个 Bucket 可挂多条 Trigger；同一事件命中多条规则时，每条规则分别生成 Task，不按目标函数合并。

## A.5 注册方式的简化

原项目应用注册、依赖注册、Bucket/Trigger 操作分属不同 RPC / Handler。V0.5 的 Demo 配置在启动前已完整确定，因此不照搬多入口控制面：

1. 上游提供完整 app.json。
2. 调用 register_app 一次注册一个 app_name。
3. App Registry 内部解析 JSON、校验函数和 Trigger 引用、构建 Function Index 与 Trigger Index。
4. 注册成功后配置只读；不开放单独的 Function/Trigger 注册 API。

这样可以避免函数注册成功但 Trigger 注册失败、多个 JSON 覆盖不一致、注册顺序依赖等问题。

## A.6 与原项目不可混淆的地方

- V0.5 不支持 DIRECT，即使原 Pheromone 支持也不代表本期保留。
- V0.5 只有一个 app.json，不使用 function.json。
- V0.5 的 Task Coroutine Pool 属于下游，不在 Task Router 内实现。
- V0.5 的 Inline / Shared-Memory 载体由 Data SDK 决定，不复制 Pheromone 的小对象打包机制。
- V0.5 的固定 SO 入口是 handler，不是原项目的 handle。
- V0.5 的 SO 固定在 /dev/shm，由下游框架自动加载；Scheduler 不 dlopen。
