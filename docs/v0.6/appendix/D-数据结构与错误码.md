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
    class FunctionInfo {
      so_name
      func_name
      Resource
    }
    class DataReadyEvent {
      bucket
      key
      session_id
    }
    class TaskArgument {
      key
      data_addr
      data_size
    }
    class TaskInvocation {
      session_id
      FunctionInfo
      input_mode
      Args
    }
    AppRecord --> FunctionInfo
    AppRecord --> DataReadyEvent
    DataReadyEvent --> TaskInvocation
    FunctionInfo --> TaskInvocation
    TaskInvocation --> TaskArgument
~~~

DataReadyEvent 是上游借用视图；TaskInvocation 是提交给 Pool 的最小执行描述。TaskAction、RouterPendingTask 是内部对象，不属于上游 API。

## D.2 DataReadyEvent 与通知参数

DataReadyEvent 只有三个字段：

| 字段 | 必须 | 说明 |
|---|---|---|
| Bucket | 是 | 全局唯一 Bucket |
| Key | 是 | 本次提交对象 Key |
| SessionID | 是 | 业务 Session |

EventTriggerNotify 独立接收 sourceFuncName、dataAddr、dataSize。外部 Actor 的 sourceFuncName 为空字符串。

不传 AppName、EventID、Trigger 配置、目标函数、PayloadType、DataRef、IP 或 RPC 地址。

## D.3 TaskInvocation 最小字段

| 字段 | 必须 | 说明 |
|---|---|---|
| SessionID | 是 | 业务 Session |
| Function.SOName | 是 | 下游识别的 SO 文件名 |
| Function.FuncName | 是 | 目标函数标识 |
| Function.Resource | 是 | CPU / Memory 资源需求 |
| InputMode | 是 | InputValue / InputKeyValue / InputKeyOnly |
| Args | 是 | 参数数组；当前长度为 1 |

每个 TaskArgument 包含必填 Key、DataAddr 和 DataSize。DataAddr 在 DataSize 大于 0 时必须非空。

## D.4 输入模式

| 路径 | 输入模式 | Function 输入语义 |
|---|---|---|
| DIRECT | InputValue | Value |
| Trigger option 0 | InputValue | Value |
| Trigger option 1 | InputKeyValue | Key + Value |
| Trigger option 2 | InputKeyOnly | Key Only |

Key 在 TaskArgument 中始终必填。InputMode 只决定 Function 输入视图，不决定数据载体。

## D.5 数据地址所有权

1. 上游 Data SDK 在 Session 生命周期内持有数据内存。
2. EventTriggerNotify 只借用 dataAddr，不复制、不释放。
3. Task Builder 为每个目标构建独立 TaskArgument 描述符，但共享同一 dataAddr。
4. Router 在 Delay Queue 中保留 TaskInvocation 元数据副本，不拥有数据内存。
5. Pool Accepted 后接收任务描述符副本，不接管 dataAddr 所有权。
6. Pool / Router 均不得 free、munmap 或释放 dataAddr。
7. Session 结束且该 Session 的所有相关任务完成或终止后，上游统一回收数据。
8. 一个 Task 完成不能导致同一 Session 其他 Task 正在使用的数据被回收。

## D.6 DIRECT 与 Trigger 路径

- sourceFuncName 有 DIRECT targets：构建 DIRECT Task，跳过 Bucket Trigger。
- sourceFuncName 为空或无 DIRECT targets：执行 Bucket Trigger 匹配。
- 一个 DIRECT source 对应多个 target 时，每个 target 生成独立 Task。
- 一个 Bucket 多条 Trigger 命中时，每条规则生成独立 Task，不去重。
- DIRECT 默认 InputValue；Trigger 按 trigger_option 决定输入模式。

## D.7 Pool 返回值与 Router 行为

| Pool 返回 | Router 行为 | 是否入队 |
|---|---|---:|
| Accepted | 删除 Router PendingTask；Pool 接管任务描述符副本 | 是 |
| NoCapacity + Delay enabled | 放入 Delay Queue | 否 |
| NoCapacity + Delay disabled | 记录 PROCESS_DISPATCH_REQUIRED，释放 Router 元数据 | 否 |
| Error | 终止并记录内部错误 | 否 |
| Delay expired | 记录 TASK_DELAY_EXPIRED，释放 Router 元数据 | 否 |

CPU / Memory 资源不足发生在 TryEnqueue 之前，不调用 Pool，也不进入 Delay Queue。

## D.8 错误语义

EventTriggerNotify 使用 Go error：

| 类别 | 语义 |
|---|---|
| nil | 通知处理流程正常完成；无 Trigger 命中不是错误 |
| ErrNotInitialized | 模块尚未初始化 |
| ErrInvalidArgument | 参数为空、格式错误、Bucket 不存在或 sourceFuncName 不属于当前 App |
| ErrInternal | 内部不变量破坏或不可恢复错误 |

TriggerInit 的配置读取、JSON 解析、字段校验和索引构建错误通过 error 返回，错误信息应包含文件路径和字段路径。

## D.9 Delay Queue 配置和状态

Delay Queue 为进程级全局配置。多个 App 声明的配置必须完全相同。具体字段：

- enabled：是否启用。
- retry_interval_ms：两次提交尝试的最小间隔。
- max_wait_ms：从首次 NoCapacity 到终止的最大等待时长。

时间使用单调时钟。队列达到实现级容量上限时，拒绝新项并释放 Router 持有的元数据。Delay Queue 只重试提交，不重新执行已经进入 Pool 的 Function。
