# 附录 A：Pheromone 源码映射与裁剪说明

参考 [MincYu/pheromone](https://github.com/MincYu/pheromone/tree/4d22677aa20dd94069da06d47ec2608504bf6bf4)，固定提交 4d22677aa20dd94069da06d47ec2608504bf6bf4。本附录记录原项目真实行为；原项目能力不等于 V0.4 研发需求。

## A.1 源码索引

| 原能力 | 源码证据 | V0.4 处理 |
|---|---|---|
| Coordinator / Local Scheduler 两级架构 | [README](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/README.md) | 仅继承本地优先思想 |
| 全局 Worker 选择 | [func_call_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/func_call_handler.cpp) | 不实现 |
| 数据位置感知与全局 Trigger | [notify_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/notify_handler.cpp) | 不实现；仅一个本地 Event Trigger |
| 本地冷热执行实例选择、延迟/上送 | [scheduler_server.cpp L129–216](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp#L129-L216) | 不实现实例选择；只提交本地池 |
| 应用注册与依赖 | [operation.proto](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/proto/operation.proto#L21-L50)、[app_register_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/app_register_handler.cpp) | JSON 静态注册 |
| 本地 DIRECT 邻接表 | [comm_helper.hpp L275–290](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/include/scheduler/comm_helper.hpp#L275-L290) | 源×目标独立边（本项目扩展） |
| DIRECT 输出分支 | [scheduler_server.cpp L345–426](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp#L345-L426) | 保留源级优先、可选目标、fan-out |
| BucketKey / direct_invoc | [common.hpp L20–85](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/include/common.hpp#L20-L85) | 保留逻辑标识和保留 Bucket，不照搬字符串拼接 |
| Bucket 创建删除 | [bucket_op_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/bucket_op_handler.cpp) | SDK 管理对象，Scheduler 只注册名称 |
| Trigger 配置与下发 | [operation.proto L137–273](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/proto/operation.proto#L137-L273)、[trigger_op_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/trigger_op_handler.cpp) | 启动 JSON 注入，不做动态增删 |
| Immediate | [trigger.hpp L63–98](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/include/trigger.hpp#L63-L98) | 无状态，每次事件命中 |
| ByName | [trigger.hpp L100–145](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/include/trigger.hpp#L100-L145) | 原始 Key 精确比较 |
| TriggerAction / 参数还原 | [object_handlers.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/object_handlers.cpp) | Action→TaskDescriptor，统一引用 |
| 对象创建、共享内存、输出通知 | [function_lib.hpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/include/executor/function_lib.hpp) | Data SDK 负责实现 |
| SO 加载与执行 | [executor_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/executor/executor_server.cpp)、[cpp_function.hpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/include/function_interface/cpp_function.hpp) | 下游负责；本项目入口改为 handler |

## A.2 两级调度：只保留调研结果

原 Coordinator 根据可用执行资源选择 Worker；数据通知路径优先考虑数据所在且有可用 Executor 的节点。Local Scheduler 的 schedule_func_call 先选择已加载该函数的可用 warm executor，否则选择冷实例或共享可用实例；无可用实例时可进入延迟队列或转发 Coordinator，受配置限制。它面对多个独立 Executor，而本项目只面对一个由下游提供的 TaskQueue。V0.4 不需要 Coordinator、冷热选择、状态表、负载均衡或数据位置路由。队列满仅记录需要未来进程间分发的日志，返回未接收。

原本地 Trigger 匹配后随 notify_put 上报 active trigger names；Coordinator 处理通知时跳过本地已生效的规则，避免双侧重复调度。这是分层协同，不是通用事件 ID 去重。当前唯一 Event Trigger 不需要复制这套状态和通信路径。

## A.3 DIRECT 与数据触发

operation.proto 将 DIRECT 与 PrimitiveType 分开；Coordinator 注册时保留 DIRECT，其他部分依赖类型转换为 Bucket Trigger。本地 comm_helper 取每条 DIRECT 的 src_functions(0)，将全部 tgt_functions 插入集合；因此不将原代码描述为原生多源展开。本项目按源×目标展开是明确的自研配置语义，非多源汇聚。

scheduler_server 的临时对象输出路径先看源函数是否存在 DIRECT；未指定目标时扇出，指定目标时检查合法性。即使对象来自普通 Bucket，只要该源存在 DIRECT 就不检查普通 Trigger。无边时且 Bucket 非 direct_invoc 才走 Bucket Trigger。当前保留该优先级并明确非法目标返回错误；普通外部 Actor 允许空源。原项目还有持久输出/客户端响应等不同消息路径，本期不移植。

## A.4 Primitive 能力与裁剪

| Primitive | 源码语义 | 当前 |
|---|---|---|
| Immediate | 每对象产生 Action | P0 |
| ByName | key == key_name 才产生 Action | P0 |
| ByBatchSize | 累计对象到阈值（不按 Session 隔离） | 不做 |
| BySet | Session 内收齐指定 key 集合 | 不做 |
| DynamicGroup | 按冒号前缀分组，控制 key 决定触发 | 不做 |
| Redundant | K/N 冗余，首 K 个对象触发 | 不做 |
| ByTime | 到达事件时检查时间间隔，非独立定时器 | 不做 |

Immediate/ByName 源码还处理 rerun 缓存，clear 为 no-op；本期不移植自动重跑。原 Trigger Option 注释 0 value、1 key+value、2 key only，实际 object_handlers 使用 arg_flag=option+1，缓存小值时可走 arg_flag=0，不能直接等同完整 Handler ABI。本项目保留三种业务语义，统一 DataRef。

## A.5 数据与执行边界

原函数库以共享内存对象创建/访问及 IPC 通知为主，小对象可能打包进消息。BucketKey 的共享内存命名拼接使用 session，本项目不复制这一实现或要求固定长度 session。原共享内存回收相关代码存在 TODO，不应当作完备 GC 保证。自研 DataRef 生命周期是上下游接口要求，而不是声称原项目已有同款引用计数。

原 executor 的入口符号为 handle，签名依赖 C++ UserLibraryInterface。当前使用 C11，入口由用户规定为 handler，/dev/shm/tasks 为本项目固定路径，真实加载由协程池团队负责。

## A.6 版本差异结论

原项目的分布式功能仅保留在此附录及第 2 章源码对照，不进入实现模块、JSON 调度策略、P0 需求、工程目录或测试验收。V0.4 以本项目单进程 Demo 为准。
