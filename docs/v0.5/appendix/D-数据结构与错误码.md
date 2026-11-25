# 附录 D：数据结构、所有权与错误码

以 [公共头文件](../examples/task_scheduler.h) 和 [接口章节](../03-接口与协作设计.md) 为准。本附录补充内部关系和生命周期约束。

## D.1 核心对象关系

~~~mermaid
classDiagram
    class AppRecord {
      app_name
      FunctionIndex
      TriggerIndex
    }
    class FunctionConfig {
      so_name
      handler
      resource
      execution
    }
    class TriggerConfig {
      bucket
      trigger_name
      primitive_type
      target_function
      key_name
      input_mode
    }
    class DataReadyEvent {
      event_id
      app_name
      bucket / key / session
      payload_type
      data_size
      inline_data / data_ref
    }
    class TaskAction {
      trigger
      target_function
      event_view
    }
    class TaskDescriptor {
      task_id / event_id
      so_name / handler
      input_mode
      inline_copy / data_ref
    }
    AppRecord --> FunctionConfig
    AppRecord --> TriggerConfig
    DataReadyEvent --> TaskAction
    TaskAction --> TaskDescriptor
~~~

一个 AppRecord 内含 Function Index 和 Trigger Index；没有 Dependency Registry。一个 Bucket 可索引到多个 TriggerConfig。

## D.2 Payload 与输入模式

| input_mode | Event Payload | TaskDescriptor |
|---|---|---|
| INPUT_VALUE | INLINE | Inline 字节副本 |
| INPUT_VALUE | SHARED_MEMORY | DataRef |
| INPUT_KEY_VALUE | INLINE | Key + Inline 字节副本 |
| INPUT_KEY_VALUE | SHARED_MEMORY | Key + DataRef |
| INPUT_KEY_ONLY | INLINE | Key，不保留 Payload |
| INPUT_KEY_ONLY | SHARED_MEMORY | Key，不 retain DataRef |

Payload Type 由 Data SDK 决定；Input Mode 由 Trigger 的 trigger_option 决定。二者是不同维度。

## D.3 所有权矩阵

| 对象 | 创建/提供方 | 持有方 | 释放时机 |
|---|---|---|---|
| app.json 原始缓冲区 | 上游 | register_app 借用 | register_app 返回后上游可释放 |
| AppRecord / Index | App Registry | App Registry | destroy 且无在途 notify 后 |
| DataReadyEvent | Data SDK | notify 调用借用 | notify 返回 |
| Inline 事件缓冲区 | Data SDK | notify / Task Builder 临时借用 | notify 返回 |
| Task 内 Inline 副本 | Task Builder | TaskDescriptor / Pool | Task 执行清理后 |
| DataRef 基础引用 | Data SDK | SDK | notify 返回后按约定 release |
| 每 Task DataRef retain | Task Builder | Router 临时持有 | 入队成功转 Pool；拒绝由 Router release |
| Pool TaskDescriptor 副本 | Task Pool | Task Pool | 执行完成/失败/取消后 |
| TaskContext / value view | Task Pool | handler 借用 | handler 返回 |
| SDK / Pool ctx | 对应团队 | 所有 Adapter 调用 | 覆盖全部回调和在途 Task 生命周期 |

## D.4 多 Trigger 与引用计数

一个事件命中 N 条规则时，按规则逐项构建：

- INLINE 且需要 value：复制 N 份（每条命中规则一份）。
- SHARED_MEMORY 且需要 value：retain N 次；每个 Task 有独立引用。
- KEY_ONLY：不复制、不 retain。
- 入队成功的 DataRef 由 Pool 释放；队列拒绝的 DataRef 由 Router 释放。
- 一条规则失败不回滚其他规则成功项。

测试结束后，Mock Data SDK 中每个 DataRef 的基础引用和 Task 引用均应归零。

## D.5 覆盖写与版本稳定性

同 Bucket/Key 的再次 send_object 可能覆盖当前 Key 内容，但若没有 DataReadyEvent，Scheduler 不会收到通知，也不会触发新 Task。

已生成 Task 的 DataRef 必须绑定原 Commit 的不可变版本。若 SDK 使用固定槽覆盖，必须通过版本化对象、Copy-on-Write、引用保护或等价方式保证旧 DataRef 在 release 前仍有效。Scheduler 不通过 Bucket/Key 重新读取最新数据，也不自行实现对象快照。

## D.6 错误码

| 值 | 名称 | 语义 |
|---:|---|---|
| 0 | PM_OK | 事件处理完成，检查逐项结果 |
| -1 | PM_ERR_INVALID_ARGUMENT | 参数为空或格式错误 |
| -2 | PM_ERR_CONFIG | app.json 解析或校验失败 |
| -3 | PM_ERR_DUPLICATE_APP | app_name 已注册 |
| -4 | PM_ERR_UNKNOWN_APP | app_name 未注册 |
| -5 | PM_ERR_UNKNOWN_BUCKET | Bucket 未在 App 中声明 |
| -6 | PM_ERR_UNKNOWN_FUNCTION | Trigger 目标或来源函数未知 |
| -7 | PM_ERR_INVALID_PAYLOAD | Payload Type 与字段不一致/超出容量 |
| -8 | PM_ERR_DATA_REF | DataRef validate / retain 失败 |
| -9 | PM_ERR_RESULT_CAPACITY | 结果容量不足；count 返回所需数量且无投递副作用 |
| -10 | PM_ERR_NO_MEMORY | 注册或构建时内存不足 |
| -11 | PM_ERR_REGISTRY_SEALED | Registry 已冻结，禁止新注册 |
| -12 | PM_ERR_INTERNAL | 内部不变量破坏或 ID 耗尽 |

未知 App、Bucket、Function 和非法 Payload 必须区分。合法未命中返回 PM_OK + matched=0。

## D.7 Pool / Router 状态

| Pool 返回 | Router 结果 | 已入队 | DataRef 所有者 |
|---|---|---:|---|
| ENQUEUE_OK | ROUTE_LOCAL_ENQUEUED | 是 | Pool |
| ENQUEUE_QUEUE_FULL | ROUTE_QUEUE_FULL | 否 | Router release |
| ENQUEUE_NOT_READY / RESOURCE_UNAVAILABLE | ROUTE_POOL_UNAVAILABLE | 否 | Router release |
| ENQUEUE_INVALID_TASK | ROUTE_INVALID_TASK | 否 | Router release |
| ENQUEUE_INTERNAL_ERROR | ROUTE_ERROR | 否 | Router release |

入队成功后 wake_one：

| wake 返回 | 语义 | 是否改变入队结果 |
|---|---|---|
| WAKE_SIGNALED | 唤醒一个等待协程 | 否 |
| WAKE_NO_WAITER | 当前没有等待协程，Task 留在队列 | 否 |
| WAKE_ERROR | 唤醒异常，需日志/告警；队列仍持有 Task | 否 |

QUEUE_FULL 只打印 PROCESS_DISPATCH_REQUIRED，日志 dispatched=false。本期没有实际进程间分发。

## D.8 事件结果计数

对结果缓冲区足够的合法事件：

- matched：命中的 Trigger 数。
- enqueued：try_enqueue 返回 ENQUEUE_OK 的数量。
- overflow：try_enqueue 返回 ENQUEUE_QUEUE_FULL 的数量。
- failed：Task 构建失败或 Pool 其他拒绝数量。

必须满足 `matched = enqueued + overflow + failed`。wake_one 的 WAKE_NO_WAITER / WAKE_ERROR 不改变 enqueued。零匹配不是错误。

若结果缓冲区不足，先返回 PM_ERR_RESULT_CAPACITY 和所需 count；不 retain、不构建、不入队。调用者扩容后可安全重调。
