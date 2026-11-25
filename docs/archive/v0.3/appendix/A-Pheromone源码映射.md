# 附录 A：Pheromone 源码映射与裁剪说明

## A.1 参考版本

- 仓库：https://github.com/MincYu/pheromone
- Commit：4d22677aa20dd94069da06d47ec2608504bf6bf4
- README 声明 Pheromone 使用两级分布式调度，并在 Worker Node 内通过共享内存做数据交换。
- 该版本源码为 C++，本项目只继承其设计语义，不直接复制 C++ 实现。

## A.2 核心源码索引

| 能力 | 源码文件 | 关键观察 | 本项目处理 |
|---|---|---|---|
| 两级调度总览 | [README.md](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/README.md) | Coordinator + Local Scheduler、共享内存数据交换 | 拆分为 Node / Process / Coroutine 三级 |
| Trigger 抽象 | [common/include/trigger.hpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/include/trigger.hpp) | TriggerAction、TriggerOption、Immediate/ByName 等实现 | 只实现 ImmediateTrigger |
| 依赖和协议 | [common/proto/operation.proto](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/proto/operation.proto) | DependencyType、DataDependency、Primitive、FunctionCall、TriggerEntity | 定义 C 结构体和 JSON 配置 |
| 应用注册 | [src/coordinator/app_register_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/app_register_handler.cpp) | DIRECT 依赖保留；其他依赖转换为 Bucket Trigger | DIRECT 单独维护，Trigger 只配置 Immediate |
| Bucket 管理 | [src/coordinator/bucket_op_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/bucket_op_handler.cpp) | 创建/删除 Bucket 和应用映射 | V0.3 静态配置初始化 |
| Trigger 管理 | [src/coordinator/trigger_op_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/trigger_op_handler.cpp) | ADD/DELETE Trigger 并向 Local Scheduler 同步 | 动态管理列为 P1 |
| Local Trigger 匹配 | [src/scheduler/object_handlers.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/object_handlers.cpp) | 对 Bucket 下 Trigger 调用 action_for_new_object，生成函数参数元数据 | Event Trigger + Task Builder |
| Local Scheduler | [src/scheduler/scheduler_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp) | DIRECT 分支先于普通 Trigger；Warm/Cold Executor 选择 | DIRECT 优先；Warm Process 优先 |
| Local Registry 同步 | [include/scheduler/comm_helper.hpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/include/scheduler/comm_helper.hpp) | 接收 UpdateCoordMsg，更新依赖、Trigger 和清理状态 | 只读 RegistrySnapshot；动态更新后续扩展 |
| 全局数据通知调度 | [src/coordinator/notify_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/notify_handler.cpp) | 维护 BucketKey 地址；跳过已在本地触发的 Trigger；优先数据所在且有容量的节点 | Node Scheduler 数据位置优先 |
| 全局函数调用调度 | [src/coordinator/func_call_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/func_call_handler.cpp) | 根据节点可用 Executor 数分配调用 | Node Scheduler 容量策略参考 |
| Bucket 数据对象 API | [include/executor/function_lib.hpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/include/executor/function_lib.hpp) | create_object、send_object、get_object；小对象可打包，大对象通过共享内存标识传递 | Data SDK 提供 DataRef；Scheduler 不复制 Payload |
| Function ABI | [include/function_interface/cpp_function.hpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/include/function_interface/cpp_function.hpp) | C 导出 handle，但参数接口本身是 C++ 类 | 本项目定义纯 C ABI |

说明：以上链接指向参考 commit。若 GitHub 文件路径发生变化，以该 commit 的 tree 为准。

## A.3 DIRECT 与 Trigger 的源码事实

Pheromone 的 app_register_handler 对 DIRECT 类型依赖直接加入下发的 dependencies；MANY_TO_ONE、K_OUT_OF_N、PERIODIC 等类型则转换为 Bucket 和对应 Trigger。Local Scheduler 收到依赖后建立 source_function → target_functions 映射。

在 scheduler_server 的输出处理路径中，若 source_function 在 direct_deps_ 中，则进入 DIRECT Invocation；只有不满足该条件且 Bucket 不是专用 Direct Invocation Bucket 时，才执行普通 Trigger 检查。这意味着 DIRECT 与 Bucket Trigger 在该代码路径上不是同一个抽象，DIRECT 分支优先。

本项目保持这一语义：DIRECT 先查依赖边；普通数据事件再查 Bucket ImmediateTrigger。V0.3 不允许同一个输出在未声明策略的情况下双路触发。

### A.3.1 Local Trigger 与 Coordinator Trigger 的协同

Pheromone 的 Local Scheduler 在本地收到对象写入消息后先执行 Trigger 匹配；若某些 Trigger 已在本地生成 Action，会把 active_trigger_names 随 notify_put 请求发送给 Coordinator。Coordinator 收到通知后仍会遍历 Bucket Trigger，但会跳过 active_trigger_names 中已在本地生效的规则，避免同一条规则在 Local Scheduler 和 Coordinator 两侧重复调度；其他规则仍可由 Coordinator 完成全局触发与节点选择。Local Scheduler 和 Coordinator 还通过 Trigger 更新消息同步规则状态。

V0.3 不复制这套双侧匹配机制：DataReadyQueue 由目标 Scheduler Process 消费，Event Trigger 是唯一规则匹配点；匹配结果构建成 TaskDescriptor 后再交给 Node Scheduler。Node Scheduler 只负责节点选择，不再二次调用 ImmediateTrigger。该收敛减少状态复制和跨层同步，是当前预研 Demo 的明确简化点。

## A.4 ImmediateTrigger 源码行为

Pheromone ImmediateTrigger 的 action_for_new_object：
- 清理该 Session 的 rerun 状态缓存。
- 生成一个 TriggerAction。
- TriggerAction.function 为目标函数。
- TriggerAction.session_keys 仅包含当前事件的 Session + Key。

它没有批量聚合状态。V0.3 采用相同触发语义，但将输入由 Key/Value 参数升级为 DataRef，避免 Scheduler 复制业务数据。

## A.5 TriggerOption 语义

Pheromone Trigger 基类注释定义三种参数形态：Value、Key + Value、Key Only。Local Scheduler 会根据数据是否在本地缓存中决定是传真实值，还是传对象名/长度等引用元信息。

V0.3 保留三种业务输入语义，但实际 TaskDescriptor 始终携带 DataRef；是否向 Handler 暴露数据读取能力由 input_mode 决定。这样不会因触发模式不同而破坏共享内存引用生命周期。

## A.6 数据位置与全局调度

Pheromone Coordinator 在收到数据通知时记录 BucketKey 的数据地址和节点；触发函数时优先从数据所在节点中选择有可用 Executor 的节点，找不到时再考虑其他有可用 Executor 的节点。跨节点数据访问由 Pheromone 的数据访问机制支撑。

本项目只有在 Data SDK 明确支持远程 DataRef 解析时才允许非数据本地路由；否则必须保留数据本地性约束。

## A.7 Pheromone 行为与本项目差异

| 维度 | Pheromone | Pheromone-mini V0.3 |
|---|---|---|
| 语言 | C++ | C11 核心 + C ABI |
| 调度层级 | Coordinator + Local Scheduler 两级 | Node + Process + Coroutine 三级 |
| Trigger | 多种 Primitive | 仅 ImmediateTrigger |
| DIRECT | 显式依赖路径 | 显式依赖路径 |
| Bucket | Bucket/Key/Session | Bucket/Key/Session + DataRef |
| 数据传递 | 小对象可打包，大对象可共享内存引用 | Scheduler 始终只传 DataRef |
| 配置 | Protobuf 注册/控制消息等 | app.json + func.json |
| 动态 Trigger 管理 | 支持 ADD/DELETE 控制路径 | P1，V0.3 静态配置 |
| 执行器 | Executor | Task Coroutine Pool |
| 第三层 | 无独立协程池调度层定义 | 由 Pool 提供线程/协程级队列和唤醒 |

## A.8 源码审查注意事项

源码中存在 TODO、注释掉的路径和特定实验实现，不能把每一段代码都理解为正式保证。例如动态组 Trigger 的 Session 支持有 TODO；ByTimeTrigger 的检查与数据到达路径耦合；部分清理函数只清理局部缓存。V0.3 不继承这些非必要边缘能力。
