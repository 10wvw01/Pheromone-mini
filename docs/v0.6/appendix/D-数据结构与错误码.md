# 附录 D：数据结构、所有权与错误码

## D.1 核心对象关系

~~~mermaid
classDiagram
    class AppRecord {
      app_name
      FunctionIndex
      BucketIndex
      DirectDependencyIndex
      TriggerIndex
      DelayQueueConfig
    }
    class BucketRecord {
      bucket_name
      owning_app
      TriggerList
    }
    class DirectDependency {
      source_so_name
      target_so_names
    }
    class TriggerConfig {
      trigger_name
      primitive_type
      target_so_name
      key_name
      trigger_option
    }
    class DataReadyEvent {
      bucket
      key
      session_id
      source_function
      payload_type
      data_size
      inline_data_or_data_ref
    }
    class TaskInvocation {
      so_name
      session_id
      input_mode
      key
      payload_type
      data_size
      inline_data_or_data_ref
    }
    AppRecord --> BucketRecord
    AppRecord --> DirectDependency
    AppRecord --> TriggerConfig
    DataReadyEvent --> TaskInvocation
~~~

DataReadyEvent 是上游借用视图；TaskInvocation 是提交给 Pool 的最小执行描述。TaskAction、RouterPendingTask 是内部对象，不属于 ABI。

## D.2 DataReadyEvent 最小字段

| 字段 | 必须 | 说明 |
|---|---|---|
| bucket | 是 | 全局唯一 Bucket |
| key | 是 | 本次提交对象 Key |
| session_id | 是 | 业务 Session |
| source_function | 条件必需 | Function 输出时由 SDK 注入；外部 Actor 可空 |
| payload_type | 是 | INLINE / SHARED_MEMORY |
| data_size | 是 | 本次提交数据长度 |
| inline_data / inline_size | INLINE 必须 | 临时借用数据 |
| data_ref | SHARED_MEMORY 必须 | Data SDK 不透明引用 |

不传 app_name、event_id、Trigger 配置、目标函数、IP、RPC 地址。内部 trace_seq 由本模块生成。

## D.3 TaskInvocation 最小字段

| 字段 | 必须 | 说明 |
|---|---|---|
| so_name | 是 | 下游加载的 SO 文件 |
| session_id | 是 | 业务 Session |
| input_mode | 是 | VALUE / KEY_VALUE / KEY_ONLY |
| key | 条件必需 | KEY_VALUE / KEY_ONLY |
| payload_type | 是 | NONE / INLINE / SHARED_MEMORY |
| data_size | 是 | 输入长度；NONE 为 0 |
| payload | 条件必需 | Inline 副本或 DataRef |

不传 app_name、bucket、task_id、event_id、source_function、trigger_name、source_type、resource、execution、handler 名称。

## D.4 输入模式和 Payload

| 路径 | 输入模式 | INLINE | SHARED_MEMORY |
|---|---|---|---|
| DIRECT | INPUT_VALUE | 复制 Value | retain DataRef |
| Trigger option 0 | INPUT_VALUE | 复制 Value | retain DataRef |
| Trigger option 1 | INPUT_KEY_VALUE | 复制 Key + Value | Key + retain DataRef |
| Trigger option 2 | INPUT_KEY_ONLY | 仅 Key | 仅 Key，不 retain |

Payload Type 由 Data SDK 决定；Input Mode 由 DIRECT 规则或 Trigger Option 决定。

## D.5 DataRef 所有权

1. Data SDK 在通知期间持有基础引用。
2. Task Builder 对每个需要 Value 的 Task 独立 retain。
3. Router 获得 TaskInvocation 后持有该引用。
4. Pool 返回 ACCEPTED 后接管引用。
5. Pool 拒绝时不接管；若进入 Delay Queue，Router 继续持有。
6. Delay Queue 成功重试后 Pool 接管；超时、关闭或终止错误由 Router release。
7. KEY_ONLY 不 retain。
8. 所有引用必须最终归零。

## D.6 DIRECT 与 Trigger 路径

- source_function 有 DIRECT targets：构建 DIRECT Task，跳过 Bucket Trigger。
- source_function 为空或无 DIRECT targets：执行 Bucket Trigger 匹配。
- 一个 DIRECT source 对应多个 target 时，每个 target 生成独立 Task。
- 一个 Bucket 多条 Trigger 命中时，每条规则生成独立 Task，不去重。
- DIRECT 默认 INPUT_VALUE；Trigger 按 trigger_option 决定输入模式。

## D.7 Pool 返回码与 Router 行为

| Pool 返回 | Router 行为 | 是否入队 | DataRef 归属 |
|---|---|---:|---|
| ACCEPTED | 删除 Router PendingTask | 是 | Pool |
| NO_CAPACITY + Delay enabled | 放入 Delay Queue | 否 | Router |
| NO_CAPACITY + Delay disabled | 记录 PROCESS_DISPATCH_REQUIRED，释放 | 否 | 释放 |
| NOT_READY | 终止并记录错误 | 否 | 释放 |
| INVALID_TASK | 终止并记录错误 | 否 | 释放 |
| ERROR | 终止并记录错误 | 否 | 释放 |
| Delay expired | 记录 TASK_DELAY_EXPIRED | 否 | 释放 |

## D.8 错误码

| 值 | 名称 | 语义 |
|---:|---|---|
| 0 | PM_OK | 处理完成；未命中不是错误 |
| -1 | PM_ERR_INVALID_ARGUMENT | 参数为空或格式错误 |
| -2 | PM_ERR_CONFIG | app.json 解析或校验失败 |
| -3 | PM_ERR_DUPLICATE_APP | app_name 已注册 |
| -4 | PM_ERR_DUPLICATE_BUCKET | Bucket 全局重复 |
| -5 | PM_ERR_DUPLICATE_FUNCTION | Function / SO 冲突 |
| -6 | PM_ERR_UNKNOWN_BUCKET | 未注册 Bucket |
| -7 | PM_ERR_UNKNOWN_FUNCTION | 未注册来源或目标 SO |
| -8 | PM_ERR_INVALID_DEPENDENCY | DIRECT 配置非法 |
| -9 | PM_ERR_INVALID_TRIGGER | Trigger 配置非法 |
| -10 | PM_ERR_INVALID_PAYLOAD | Payload 字段不一致 |
| -11 | PM_ERR_DATA_REF | DataRef validate / retain 失败 |
| -12 | PM_ERR_NO_MEMORY | 内存不足 |
| -13 | PM_ERR_REGISTRY_SEALED | Registry 已 seal |
| -14 | PM_ERR_POOL_REGISTER | Pool 静态 Function 注册失败 |
| -15 | PM_ERR_INTERNAL | 内部不变量破坏 |

## D.9 Delay Queue 配置和状态

Delay Queue 为全局配置：

- enabled：是否启用。
- retry_interval_ms：两次提交尝试的最小间隔。
- max_wait_ms：从首次 NO_CAPACITY 到终止的最大等待时长。

时间使用单调时钟。队列达到实现级容量上限时，拒绝新项并释放 Router 持有资源。Delay Queue 只重试提交，不重新执行已经进入 Pool 的 Function。
