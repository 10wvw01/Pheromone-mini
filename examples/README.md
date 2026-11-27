# 上游接入 E2E 示例

在仓库根目录使用 Go 1.26.2 运行：

```sh
go run ./examples
```

程序为两个 App 依次调用真实的 `api.TriggerInit`，再由示例 Data SDK 调用真实的 `api.EventTriggerNotify`。调度器执行配置解析、触发匹配和资源准入。Mock Pool 将入队与消费分开，程序逐个出队执行 Go 函数，校验任务的五个字段、批地址和最终结果。任何校验失败都会以非零状态退出。

预期看到两个 App 各执行 `prepare → transform → collect`，输出分别为 `[3 5 7]` 和 `[21 41 61]`，最后一行是 `E2E passed`。

## 场景与配置

| 阶段 | 批数据提交位置 | 命中条件 | 执行目标 |
|---|---|---|---|
| 外部 Actor 提交输入 | `input / job-001` | IMMEDIATE 匹配 `input` | `prepare.so / prepare`，逐项乘以 2 |
| `prepare` 提交下一批 | `prepared / ready` | BY_NAME 精确匹配 `ready`，`Ready` 不命中 | `transform.so / transform`，逐项加 1 |
| `transform` 提交结果 | `transformed / done` | 来源函数命中 DIRECT，跳过同 Bucket 的 `fallback` 规则 | `collect.so / collect`，校验整批结果 |

[app.json](app.json) 配置主场景，[app-copy.json](app-copy.json) 复制同一场景并修改顶层 `app_name`。两个 App 共用 Bucket 名、Key、SO 名、函数名和会话 ID，运行时按 App 独立匹配并入队。复制配置时为每份文件设置唯一的 `app_name`，保留相同的 `scheduler.delay_queue`，并在启动业务通知前逐份初始化。

`triggers[].bucket` 定义普通触发规则所在的 Bucket。BY_NAME 的 `primitive.key_name` 对应事件的 `Key`。DIRECT 由 `dependencies` 声明，`src_functions` 和 `tgt_functions` 引用当前 App 的 `functions`。同名函数有歧义时用 `{so_name, func_name}` 对象消歧。旧配置迁移时删除顶层 `buckets` 和各处 `trigger_option`。

## 上游接口

| 接口 | 调用方和时机 | 入参 | 返回值 |
|---|---|---|---|
| `api.TriggerInit(appJSONPath string) error` | 集成方在启动期调用，每份配置调用一次 | 单个 App 的 JSON 文件路径 | 成功返回 `nil`。读取失败或配置非法时返回 `*api.APIError`。首条有效通知之后再初始化会返回 `ErrInvalidState` |
| `api.EventTriggerNotify(event api.DataReadyEvent, source api.FunctionIdentity) error` | Data SDK 在整批数据提交并可读后调用 | 五字段事件和产出函数标识 | 参数无效或调度器尚未就绪时返回 `*api.APIError`。返回 `nil` 表示通知已按规则处理，可能无任务命中，也可能有任务等待入队 |

| 事件字段 | SDK 填写方式 |
|---|---|
| `AppName` | 数据所属 App，对应已注册配置的 `app_name` |
| `Bucket` | 本次写入的 Bucket 名 |
| `Key` | 批数据的业务 Key，BY_NAME 按原值匹配 |
| `SessionID` | 本次业务会话，沿任务链透传 |
| `DataAddr` | 非空地址，指向本次提交的整批数据入口 |

外部 Actor 产出时传 `api.FunctionIdentity{}`。函数产出时，SDK 从 Pool 提供的当前执行上下文取 `AppName / SOName / FuncName`。SDK 保证 `DataAddr` 及整批数据在相关任务排队、重试和执行期间有效。

## 下游接口与运行时装配

`api.TaskPool.TryEnqueue(task api.TaskInvocation) api.EnqueueResult` 按值接收 `AppName / FuncName / SOName / DataAddr / SessionID`。返回 `EnqueueAccepted` 表示已接收，`EnqueueNoCapacity` 表示暂时无容量，`EnqueueError` 表示 Pool 错误。Pool 借用批地址，数据由 SDK 管理。

[mockpool/pool.go](mockpool/pool.go) 实现带容量上限的非阻塞队列。示例在 [main.go](main.go) 中逐个出队，用 Go 分支模拟函数执行，并用固定资源快照让运行结果独立于机器负载。配置中的 SO 名用于路由和校验，示例无需编译动态库。

集成方在本仓库模块边界内用 `internal/engine.New` 装配 Pool，通过 `internal/wiring.Install` 安装运行时，随后上游才调用 `api` 包。Go 的 `internal` 可见性限制适用于装配代码，外部 Go 工程可以通过已装配的 `api` 接入业务通知。完整状态和资源准入契约见 [正式设计](../docs/DESIGN.md)。
