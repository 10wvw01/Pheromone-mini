# 上游接入 E2E 示例

在仓库根目录使用 Go 1.26.2 运行：

```sh
go run ./examples
```

默认路径由 Trigger 内部读取 Linux 资源信息。运行环境须提供可读的资源文件、可见的 cgroup 挂载及足够的 CPU 和内存。CI 或缺少这些资源接口的容器可以显式使用固定快照：

```sh
go run -tags examplefixture ./examples
```

该模式只替换资源采集结果。准入判断仍在 Router 内，在通知命中后、尝试入队前执行。

程序先调用真实的 `api.TriggerInit` 注册三个 App，再由示例 SDK 调用真实的 `api.EventTriggerNotify`。Mock Pool 逐项出队，消费任务携带的批数据地址。示例校验 10 个任务的顺序、五字段入队契约、各 App 的最终结果，以及 BY_NAME 不命中和 DIRECT 抢占普通规则的行为。任何校验失败都会以非零状态退出，成功时最后一行是 `E2E passed`。

## 三个 App 的调用链

每份 JSON 注册一个 App。三个 App 可以复用 `input/ready`、`result/ready` 和同一个会话 ID，SDK 仍按 `app_name + bucket + key` 找到各自的数据。

| App 配置 | App 内调用链 | 跨 App 数据读取 | 结果 |
|---|---|---|---|
| [app.json](app.json) | `normalize → validate → enrich → publish`，依次经 IMMEDIATE、BY_NAME、DIRECT、BY_NAME | 接收 Actor 的 `[1 2 3]`，结果供后两个 App 读取 | `[30 50 70]` |
| [app-score.json](app-score.json) | `load → weight → publish`，依次经 IMMEDIATE、DIRECT、BY_NAME | `load` 读取 `ingest/result/ready`，与本地权重 `[2 3 4]` 相乘，`weight` 再逐项加 5 | `[65 155 285]` |
| [app-report.json](app-report.json) | `join → summarize → publish`，依次经 BY_NAME、DIRECT、IMMEDIATE | `join` 读取 `ingest/result/ready` 和 `score/result/ready`，逐项相加并加上本地基数 | `[96 206 356]` |

跨 App 依赖由示例业务流程控制。等 `ingest` 的批次提交后，Actor 才启动 `score`。等 `score` 的批次也提交后，Actor 再启动 `report`。每个 App 的入口事件均携带本 App 的地址和会话，业务函数通过 SDK 显式读取其他 App 的结果。读取本身不会发送通知。产出新批次时，SDK 只在当前 App 下提交并通知，来源函数也属于当前 App。生产中的跨 App 读取权限和数据回收由实际 SDK 负责。

`triggers[].bucket` 定义普通触发规则所在的 Bucket。BY_NAME 的 `primitive.key_name` 对应事件的 `Key`，例如 `ingest/normalized/Ready` 不命中 `ready`。DIRECT 由 `dependencies` 声明，函数引用在当前 App 内解析。示例中 `ingest/validated` 同时配置了普通规则，`validate` 的 DIRECT 命中后只生成 `enrich`。旧配置迁移时删除顶层 `buckets` 和各处 `trigger_option`。

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

- [upstream.go](upstream.go) 展示上游接入。初始化及提交数据时，分别调用 `api.TriggerInit` 和 `api.EventTriggerNotify`。
- 仓库根目录的 [mockpool](../mockpool/pool.go) 是 Trigger 可注入的简易下游 Pool，实现有界、非阻塞的进程内队列。
- [main.go](main.go) 编排三个 App 的 E2E 场景，并通过验收用的消费接口逐项检查任务。
- [runner.go](runner.go) 负责本地验收环境。它创建临时 Pool 并装配调度器。收到通知且命中规则后，Trigger 内部的 Router 使用默认 Linux 探测器完成资源准入。

集成方在本仓库模块边界内用 `internal/engine.New` 装配 Pool，通过 `internal/wiring.Install` 安装运行时，随后上游才调用 `api` 包。Go 的 `internal` 可见性限制适用于装配代码，外部 Go 工程可以通过已装配的 `api` 接入业务通知。完整状态和资源准入契约见 [正式设计](../docs/DESIGN.md)。
