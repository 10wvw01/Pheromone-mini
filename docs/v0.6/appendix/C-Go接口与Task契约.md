# 附录 C：Go 接口与 Task 契约

本附录定义 V0.6 Go 语言下的公共类型和上下游契约。上游仅有 TriggerInit、EventTriggerNotify 两个 API；下游唯一接口为 TryEnqueue(TaskInvocation)。本附录不规定 Task Coroutine Pool 的 SO / Function Loader。

## C.1 基本类型

~~~go
package pheromonemini

import "unsafe"

type DataReadyEvent struct {
    Bucket    string
    Key       string
    SessionID string
}

type InputMode uint8

const (
    InputValue InputMode = iota
    InputKeyValue
    InputKeyOnly
)

type GPUResourceSpec struct {
    Required    bool
    DeviceCount uint32
    MemoryMB    uint64
}

type ResourceSpec struct {
    CPUCores    float64
    MemoryMB    uint64
    GPU         GPUResourceSpec
    DiskMB      uint64
    NetworkMbps uint64
}

type FunctionInfo struct {
    SOName   string
    FuncName string
    Resource ResourceSpec
}

type TaskArgument struct {
    Key      string
    DataAddr unsafe.Pointer
}

type TaskInvocation struct {
    SessionID string
    Function  FunctionInfo
    InputMode InputMode
    Args      []TaskArgument
}
~~~

## C.2 两个上游 API

| API ID | API |
|---|---|
| PM-API-001 | TriggerInit |
| PM-API-002 | EventTriggerNotify |

~~~go
func TriggerInit(appJSONPath string) error

func EventTriggerNotify(
    event DataReadyEvent,
    sourceFuncName string,
    dataAddr unsafe.Pointer,
) error
~~~

用户根据附录 B 自行编写 app.json。全部初始化成功结束后才允许发送通知，禁止初始化与通知并发；初始化未完成返回 ErrNotInitialized。完整状态规则见第三部分 §11.6。

TriggerInit 只接收 app.json 路径。模块自行读取 app.json，并解析内嵌 functions[]。

EventTriggerNotify 的 DataReadyEvent 只包含 Bucket、Key、SessionID。sourceFuncName、dataAddr 是独立参数。外部 SMC Actor 的 sourceFuncName 使用空字符串。

## C.3 POOL-API-001：TryEnqueue 下游接口

~~~go
type EnqueueResult uint8

const (
    EnqueueAccepted EnqueueResult = iota
    EnqueueNoCapacity
    EnqueueError
)

type TaskPool interface {
    TryEnqueue(task TaskInvocation) EnqueueResult
}
~~~

- TryEnqueue 是下游 Pool 唯一向本模块提供的接口；本模块不负责实例创建或装配。
- 首次提交和每次延时重试必须先做 CPU / Memory 检查；不足或探测 / 计算失败不调用 Pool，记录原因日志后按 scheduler.delay_queue.enabled 暂存重试或终止；固定首次可延时原因的 deadline，提交前不得到期。
- 不带 ctx 参数；Pool 实例自身持有队列和运行状态。
- 不提供 register_function、unregister_function、register_function_configs 或 wake_one。
- TaskInvocation 携带完整 FunctionInfo（SOName、FuncName、Resource），不需要 Pool 启动阶段静态注册。

## C.4 TryEnqueue 接收语义

- TryEnqueue 原子完成容量判断和入队；不等待容量恢复或任务执行完成，不在调用栈内同步执行函数。
- Accepted：Pool 接收任务，必须复制 TaskInvocation 的 Args 切片及后续需要独立持有的描述符；Go string 可共享其不可变内容。
- NoCapacity：Pool 不接管任务，返回后不得继续引用调用方可变 Args 切片。
- Error：Pool 不接管任务。
- Pool 不复制、不释放 DataAddr 指向的数据。
- Pool 内部负责队列消费、协程等待和唤醒；本模块不调用独立唤醒接口。

## C.5 Function TaskContext 逻辑契约

~~~go
type TaskContext struct {
    SessionID string
    SOName    string
    FuncName  string
    InputMode InputMode
    Args      []TaskArgument
}
~~~

TaskContext 是 Pool 与 Function / Object SDK 的逻辑执行上下文，不是本模块的第三个上游 API。Pool 应确保当前任务的 FuncName 能被 Object SDK 读取。

Function 通过 Object API 写入输出数据。Object SDK 从 TaskContext.FuncName 注入 EventTriggerNotify 的 sourceFuncName。外部 SMC Actor 不属于已注册 Function，传空字符串。

本模块不规定 Function 是 Go Plugin、cgo/native SO 还是其他执行载体；实际加载与调用 ABI 由下游 Pool 负责。

## C.6 错误语义

两个上游 API 成功返回 `nil`，失败统一返回 `*APIError`，字段为 `StatusCode`（`uint16`）和 `Msg`（非空诊断文本）。调用方以 `errors.As` 提取错误并按状态码分支。类型定义与各接口适用状态见[第三部分 §10.5.6、§10.6](../03-接口与协作设计.md)。下游 `TryEnqueue` 仍只返回 `EnqueueResult`；任务路由结果不直接转换为上游错误。

## C.7 所有权与生命周期

| 对象 | 创建方 | 使用方 | 所有权 / 有效期 |
|---|---|---|---|
| DataReadyEvent | 上游 SDK | EventTriggerNotify | 上游保证调用期间有效 |
| sourceFuncName | 上游 SDK / TaskContext | DIRECT 匹配 | 上游保证调用期间有效 |
| Key / SessionID 字符串 | 上游 SDK | Task Builder / Pool / Function | Task Builder 复制异步任务所需字符串 |
| TaskInvocation / Args | Task Builder | Router / Pool | Router 或 Pool 管理自己的描述符副本 |
| DataAddr 指向的数据 | 上游 Data SDK | Task Builder / Pool / Function | Data SDK 自行判定完成并回收；本模块不提供完成判定契约 |

不可违反的规则：

1. DataAddr 是借用地址，不发生所有权转移。
2. Scheduler 和 Pool 不得释放 DataAddr。
3. Task Builder 不复制数据内容，只构建参数描述符。
4. 一个事件命中多个 Task 时，所有 Task 都可能访问同一 DataAddr。
5. Data SDK 保证 Delay Queue 和异步执行期间借用地址有效。
6. Session 完成判定及数据回收由 Data SDK 管控，本模块不定义完成通知、引用计数或回收协议。
7. DataAddr 应指向上游管理的共享内存 / mmap 区域，不应指向未固定的 Go Heap 对象。
