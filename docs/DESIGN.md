# Pheromone-mini V0.6 完整软件设计方案

**版本：** V0.6
**状态：** 正式版
**实现语言：** Go

## 目录

- [设计概览](#设计概览)
- [第一部分：总体设计](#part-1)
- [第二部分：核心模块详细设计](#part-2)
- [第三部分：接口与协作设计](#part-3)
- [第四部分：工程实现设计](#part-4)
- [第五部分：需求追踪与测试验收](#part-5)
- [Pheromone-mini V0.6 分功能需求列表](#part-6)
- [附录 A：Pheromone 源码映射与 V0.6 裁剪说明](#part-7)
- [附录 B：app.json 配置协议与示例](#part-8)
- [附录 C：Go 接口与 Task 契约](#part-9)
- [附录 D：数据结构、所有权与错误码](#part-10)
- [附录 E：Mock 端到端测试与独立验收](#part-11)
- [V0.6 代码阅读指引](#part-12)

## 设计概览

V0.6 以 Pheromone 的两级调度、Data Bucket 和 Trigger 设计为基础，在当前项目范围内实现单节点、单进程、单调度实例的数据驱动任务触发与本地任务分发。

本模块负责业务流程③④⑤：

1. 通过 TriggerInit 加载 app.json（包含内嵌 Function 配置），解析、校验并构建内部 Registry。
2. 接收上游 EventTriggerNotify 数据就绪通知。
3. 根据 DIRECT 独立依赖或 Bucket Trigger 规则判断目标函数。
4. 根据事件携带的数据地址构建 TaskInvocation。
5. Task Router 检查目标 Function 所需的资源需求。
6. 资源满足时调用下游 Task Coroutine Pool 的 TryEnqueue；资源不足、探测或计算失败时记录日志，再判断是否启用延时调度，决定暂存重试或终止（外部上报留待后续扩展）。

本模块使用 Go 实现。Function 的 so_name 与 func_name 分别表达动态库名称和函数标识；本模块只负责配置解析、匹配和透传，不实现下游的动态库加载器。

## 设计范围

| 维度 | V0.6 冻结规则 |
|---|---|
| 实现语言 | Go |
| 调度实例 | 单进程、单个调度实例；Registry 是模块内部唯一运行实例 |
| 配置入口 | TriggerInit(appJSONPath)；模块读取 app.json，解析内嵌 functions[] |
| functions[] | app.json 内嵌 Function 配置；保存 so_name、func_name、description、version、resource |
| 对上游 API | 只暴露 TriggerInit 和 EventTriggerNotify 两个 API |
| Bucket | 在当前调度实例管理的 App 范围内全局唯一；事件不携带 app_name |
| Function 标识 | so_name 表示 SO 文件名；func_name 表示具体函数标识；两者独立 |
| Function 唯一性 | (so_name, func_name) 在当前 App 内唯一；不同共享库可使用相同 func_name |
| Function 资源 | 保留 CPU / Memory / GPU / Disk / Network Resource 字段；Task Router 必查 CPU / Memory，按需检查 GPU / Disk / Network；不定义未经确认的 Execution |
| DIRECT | 独立依赖路径；(so_name, func_name) → 目标函数标识列表 |
| Trigger | IMMEDIATE、BY_NAME |
| 多 Trigger | 一个 Bucket 可关联多条 Trigger；逐条匹配，逐条生成任务 |
| DIRECT 优先级 | 来源标识存在 DIRECT 目标时走 DIRECT；否则执行 Bucket Trigger 匹配 |
| DataReadyEvent | 仅 bucket、key、session_id |
| 通知参数 | sourceFuncName（FunctionIdentity）、data_addr 作为 EventTriggerNotify 独立参数 |
| 数据传递 | 统一通过 data_addr 传递；不区分大小数据包 |
| Task 输入 | 每次通知传一个对象，每个派生任务的 Args[] 含一项；Key 必填 |
| 输入模式 | `app.json` 使用 `trigger_option`，Go 任务使用 `TaskInvocation.InputMode`。0 = Value（默认），1 = Key + Value，2 = Key Only |
| 数据生命周期 | 上游持有数据；数据有效性、任务完成判定和回收均由 Data SDK 管控 |
| DataRef | 完全删除；不实现 validate / retain / release |
| Pool 接口 | 只有 TryEnqueue(TaskInvocation)，不传 ctx |
| Function 信息 | FunctionInfo（so_name、func_name、resource）随 TaskInvocation 传递 |
| Pool 静态注册 | 不提供，不调用 register_function_configs |
| 资源准入 | Task Router 在 TryEnqueue 前检查目标 Function 所需的资源 |
| 资源不足 | 不调用 TryEnqueue；与探测 / 计算失败一样，记录日志后按延时调度配置暂存重试或终止 |
| 队列无容量 | Pool 返回 NO_CAPACITY 后按进程级 Delay Queue 策略处理 |
| 协程调度 | Pool 内部负责队列消费和协程唤醒；本模块不调用 wake_one |
| 延时调度 | 保留进程级全局 Delay Queue；多 App 的配置必须一致 |
| 研发范围 | 单节点、单进程；不实现进程间、节点间分发 |

## 核心模块

1. **TriggerInit / JSON Parser**：读取 app.json，解析内嵌 functions[]；解析并校验 App、Function、Bucket、DIRECT Dependency、Trigger 和资源配置，建立只读索引。
2. **App Registry**：保存 App、Function、Bucket、DIRECT Dependency 和 Trigger 的内部记录及索引。
3. **DIRECT Dependency**：根据事件的来源标识 查询当前 App 的 DIRECT 目标函数，独立生成任务。
4. **Event Trigger**：根据 Bucket 索引匹配 Immediate / ByName Trigger。
5. **Task Builder**：将命中结果和事件数据地址组装为 TaskInvocation.Args[]；复制异步任务需要持有的字符串元数据，不复制数据内容。
6. **Task Router**：检查 CPU / Memory 及任务声明的 GPU / Disk / Network，资源满足后调用 TryEnqueue；处理 Pool 容量不足和 Delay Queue。
7. **Task Coroutine Pool（下游）**：只提供 TryEnqueue，管理任务队列、协程消费、SO / Function 加载和函数执行。

## 总体执行路径

~~~mermaid
flowchart TD
    A["进程启动"] --> B["TriggerInit(app.json 路径)"]
    B --> C["读取 app.json"]
    C --> D["解析内嵌 functions[]"]
    D --> E["JSON 校验 / Registry 与索引构建"]
    E --> F["初始化成功，开放事件通知"]
    F --> G["上游 Commit 成功"]
    G --> H["EventTriggerNotify(event, sourceFuncName, data_addr)"]
    H --> I["DIRECT Dependency 判断"]
    I -->|存在 DIRECT 目标| J["DIRECT：(so_name, func_name) -> 目标函数标识列表"]
    I -->|无 DIRECT 目标或来源为零值| K["Bucket Trigger：IMMEDIATE / BY_NAME"]
    J --> L["Task Builder：构建 TaskInvocation"]
    K --> L
    L --> M["Task Router：读取 FunctionInfo.Resource"]
    M --> N["检查 CPU / Memory 及任务声明的 GPU / Disk / Network"]
    N --> O{"资源是否满足？"}
    O -->|否| P["资源不足、探测或计算失败：记录日志；不调用 TryEnqueue"]
    O -->|是| Q["TaskPool.TryEnqueue(TaskInvocation)"]
    Q -->|ERROR| U
    Q -->|ACCEPTED| R["Task Coroutine Pool"]
    P --> S
    Q -->|NO_CAPACITY| S{"Delay Queue 启用？"}
    S -->|是| T["Delay Queue：等待重试，保留 deadline"]
    T --> W{"deadline 已到？"}
    W -->|否| M
    W -->|是| U
    S -->|否或超时| U["记录拒绝 / 超时并终止任务"]
    R --> V["Pool 消费队列、调度协程并执行 Function"]
~~~

DIRECT 和 Bucket Trigger 是两条独立路径，不把 DIRECT 伪装成第三种 Trigger Primitive。资源不足与队列无容量是两个不同状态：前者在调用 Pool 前拦截，后者由 Pool 的原子 TryEnqueue 返回。

## 上下游接口

### 5.1 提供给上游的两个 API

启动初始化：

~~~go
func TriggerInit(appJSONPath string) error
~~~

用户按附录 B 自行编写 app.json。TriggerInit 读取该文件并解析内嵌 functions[]。完成 JSON 解析、字段校验、配置引用校验和内部索引构建后，才发布对应 App 配置。

每个 app.json 描述一个 App。单进程需要加载多个 App 时，可在业务启动阶段多次调用 TriggerInit；每次调用按 App 粒度原子提交。所有配置初始化完成后，上游再开始调用 EventTriggerNotify。运行期开始后不再接受新的配置初始化。

运行期通知：

~~~go
type FunctionIdentity struct {
    SOName   string `json:"so_name"`
    FuncName string `json:"func_name"`
}

func EventTriggerNotify(
    event DataReadyEvent,
    sourceFuncName FunctionIdentity,
    dataAddr unsafe.Pointer,
) error
~~~

DataReadyEvent 仅包含 bucket、key、session_id。外部 SMC Actor 传 FunctionIdentity{}；Function 产生的数据由 Object SDK 从当前 TaskContext 的 SOName 和 FuncName 注入。

两个上游 API 成功返回 `nil`，失败返回 `*APIError`，包含稳定的 `status_code` 和诊断文本 `msg`。调用方用 `errors.As` 获取错误并按状态码处理。具体状态码及语义见第三部分 §10.5.6、§10.6。

### 5.2 对下游 Task Coroutine Pool 的要求

下游唯一交互为：

~~~go
type TaskPool interface {
    TryEnqueue(task TaskInvocation) EnqueueResult
}
~~~

TryEnqueue 不带 ctx。Pool 实例自身持有队列和运行状态；本模块不提供 Pool 静态 Function 注册 API。完整 FunctionInfo（so_name、func_name、resource）随 TaskInvocation 传递。

Pool 根据 so_name 和 func_name 识别执行目标；具体采用 Go Plugin、cgo/native SO 或其他加载方式，由下游 Pool 自行决定，不属于本模块的实现范围。本模块不调用 dlopen / dlsym。

## 关键数据与生命周期

- TaskArgument 包含 bucket、session_id、key、data_addr；key 必须存在。
- 当前一次 EventTriggerNotify 对应一个数据对象；每个命中目标构建独立 TaskInvocation，每个任务的 Args 长度为 1。
- TaskInvocation 包含 FunctionInfo、InputMode 和 Args[]。
- Task Builder 复制 bucket、key、session_id、so_name、func_name 等异步任务需要持有的字符串；不复制数据内容。
- data_addr 是借用地址，不发生所有权转移；调度模块和 Pool 均不得释放该地址。
- Go 接口使用 unsafe.Pointer 表示外部共享内存地址。该地址应指向上游管理的 mmap / 共享内存区域，不应指向未固定的 Go Heap 对象。
- 上游必须保证 data_addr 在 Session 生命周期内有效，且在该 Session 派生的所有任务（包括 Delay Queue 中的待提交任务）完成或终止前，不得释放或复用。
- Session 结束且相关任务全部完成或终止后，由上游 Data SDK 回收数据。
- 如果一个事件命中多个 DIRECT 目标或多条 Trigger，所有任务使用同一事件数据地址；上游必须等待全部相关任务结束后再回收。

数据有效性、Session 任务完成判定及回收均由 Data SDK 管控。本模块不新增完成判定、任务计数或回收契约；不将 SDK 内部机制列为本模块冻结前提。Pool 实例创建和装配同样不在本模块职责内，仅按接口调用。

## 资源准入与 Delay Queue

Task Router 从内部 Function Registry 获取目标 Function 的完整 ResourceSpec，并查询当前进程 / 容器 CPU、Memory 可用资源；GPU / Disk / Network 字段保留并随 FunctionInfo 透传给 Pool，Router 按工程实现设计 §14.7 检查任务实际声明的维度。

资源查询优先识别当前进程的 cgroup 限额与使用量：通过 /proc/self/cgroup 定位 cgroup，再读取 cgroup v2 或 v1 对应的 CPU / Memory 限额和使用统计；/proc/meminfo 可作为宿主机参考或兜底。资源判断是准入快照，不构成资源预留。CPU / Memory 检查是必做项，每次提交（包括延时重试）前执行；具体采样及计算规则见第四部分 §14.2。

- 资源不足：不调用 TryEnqueue，打印原因日志并判断 scheduler.delay_queue.enabled；启用则进入 / 保留 Delay Queue，未启用则终止本次路由，日志原因 RESOURCE_INSUFFICIENT。
- 资源探测 / 解析 / 准入计算失败：不调用 TryEnqueue，打印原因日志并判断 scheduler.delay_queue.enabled；启用则进入 / 保留 Delay Queue，未启用则终止本次路由，日志原因 RESOURCE_PROBE_FAILED，并用 reason 区分探测、解析和计算失败；本期不做外部上报。
- 资源满足：调用 TryEnqueue。
- Pool 返回 NO_CAPACITY：这是队列容量不足，不等同于 CPU / Memory 资源不足；按 Delay Queue 配置暂存并重试。
- Pool 返回 ACCEPTED：Pool 接收 TaskInvocation；Pool 复制任务描述符和 Args 切片，不复制、不释放 data_addr 指向的数据内容。
- Pool 返回 ERROR：终止当前任务并记录内部错误。

延时重试先判断首次资源不足、探测 / 计算失败或 Pool NoCapacity 建立的固定 deadline；到期优先终止，未到期则重新检查资源，不直接调用 TryEnqueue，不重置 deadline。

## 本期明确不实现

- DataRef、validate、retain、release 和引用计数。
- INLINE / SHARED_MEMORY 两种载体及大小阈值选择。
- Scheduler 主动调用 Data SDK 查询数据。
- Pool 静态 Function 注册、register_function_configs 和 unregister_function。
- 未经确认的 Execution 配置、resource.timeout_ms、Rerun 和自动重新执行。
- 本模块内的 SO Loader、dlopen / dlsym 或 Go Plugin 选择。
- wake_one 或任何由本模块发起的协程唤醒操作。
- 未列入核心模块详细设计的触发原语。
- Node Scheduler、Process Scheduler、Warm / Cold Executor、跨进程和跨节点分发。
- Data Bucket 创建 / 销毁、共享内存池、对象索引和底层数据搬运。
- Task Coroutine Pool、TaskQueue、工作协程和 Function 执行实现。
- 动态 Trigger 更新、Exactly-once、事件持久化和事件去重。

---

<a id="part-1"></a>

## 第一部分：总体设计


## 第 1 章 功能需求列表概述

### 1.1 业务背景与本期链路

| 步骤 | 行为 | 责任方 |
|---|---|---|
| ① | SMC Actor 处理外部消息，调用 SDK 写入数据 | 上游业务团队 |
| ② | SDK 调用 Data Bucket SDK，将 Object 写入共享内存 | 上游 Data SDK |
| ③ | 数据 Commit 成功后，上游调用 EventTriggerNotify，传入事件、来源函数和数据地址 | 上游 SDK / 本模块 |
| ④ | 本模块判断 DIRECT 或 Bucket Trigger 路径，匹配规则并构建目标函数参数 | 本模块 |
| ⑤ | Task Router 检查目标 Function 所需的资源，资源满足后调用下游 TryEnqueue | 本模块 / 下游 Pool |
| ⑥ | Task Coroutine Pool 消费队列、调度协程、加载并执行目标 Function | 下游 Pool |

本模块负责配置初始化、③④⑤，不负责 SMC Actor、Object 存储、共享内存底层、Task Coroutine Pool 的协程实现或业务函数执行。

### 1.2 本期功能范围

| 模块 | 功能点 | 优先级 | 责任方 |
|---|---|---:|---|
| TriggerInit / JSON Parser | 读取 app.json 及内嵌 functions[]，解析、校验、构建只读索引 | P0 | 本模块 |
| App Registry | Function 的 so_name / func_name / resource 配置 | P0 | 本模块 |
| DIRECT Dependency | 来源标识 (so_name, func_name) 到一个或多个 target func_name 的匹配 | P0 | 本模块 |
| Event Trigger | Immediate / ByName 匹配、多 Trigger | P0 | 本模块 |
| Task Builder | trigger_option 到 InputMode 映射、Args[] 构建 | P0 | 本模块 |
| Task Router | CPU / Memory 预检查、原子 TryEnqueue、Delay Queue | P0 | 本模块 |
| 协作接口 | TriggerInit、EventTriggerNotify、TryEnqueue | P0 | 本模块 + 上下游 |

### 1.3 研发边界与团队职责

**本模块负责：** TriggerInit、配置解析和 Registry；DIRECT；Immediate / ByName；Task Builder；CPU / Memory 预检查；TryEnqueue 调用和 Delay Queue。

**上游 Data Bucket SDK 负责：**
- Object 创建、写入和 Commit。
- 数据地址的生成与传递。
- 启动阶段调用 TriggerInit；Commit 成功后调用 EventTriggerNotify。
- Session 生命周期、任务完成判定及数据回收均由 Data SDK 管控。
- Function 输出对象时，从当前 TaskContext 注入 SOName 和 FuncName；外部 Actor 传 FunctionIdentity{}。

**下游 Task Coroutine Pool 负责：**
- 唯一提供 TryEnqueue(TaskInvocation)，不带 ctx。
- 复制任务描述符和 Args 切片，不复制、不释放 DataAddr 指向的数据。
- 队列消费、协程创建 / 等待 / 唤醒。
- 根据 SOName + FuncName 加载并执行 Function；具体 Loader 由 Pool 决定。

### 1.4 冻结的基础约束

| 约束 | 设计要求 |
|---|---|
| 语言 | Go |
| 调度实例 | 单进程、单个实例；内部唯一 Registry |
| 配置入口 | TriggerInit(appJSONPath)，加载 app.json（包含内嵌 Function 配置） |
| Function | 以 (so_name, func_name) 组合标识；不同共享库可使用同名函数 |
| Resource | 保留 CPU / Memory / GPU / Disk / Network 字段；Router 必查 CPU / Memory，按需检查 GPU / Disk / Network；不定义 Execution |
| DIRECT | 独立依赖路径；(so_name, func_name) → 目标函数标识列表 |
| Trigger | IMMEDIATE、BY_NAME |
| DataReadyEvent | 仅 bucket、key、session_id |
| 通知参数 | sourceFuncName（FunctionIdentity）、data_addr 独立传入 |
| 数据 | 统一地址传递；不区分大小数据包 |
| Task 参数 | key 必须；每个任务的 Args[] 长度为 1 |
| 数据生命周期 | Session 结束且相关任务全部完成或终止后，上游回收 |
| DataRef | 不存在；不提供 validate / retain / release |
| 上游 API | TriggerInit、EventTriggerNotify |
| 下游接口 | TryEnqueue(TaskInvocation)，无 ctx |
| Function 配置 | FunctionInfo 随 TaskInvocation 传递；无静态注册 API |
| 资源准入 | TryEnqueue 前检查 CPU / Memory |
| 协程 | Pool 内部管理消费和唤醒；本模块不调用 wake_one |

### 1.5 本期明确不实现

- Rerun、Function 执行超时、resource.timeout_ms。
- wake_one 或由本模块发起的协程唤醒。
- 未列入核心模块详细设计的触发原语。
- Node / Process Scheduler、Warm / Cold Executor、跨进程和跨节点任务分发。
- Data Bucket 底层存储、共享内存池和底层搬运。
- DataRef、引用计数和 Scheduler 主动调用 Data SDK 查询数据。
- Pool 静态 Function 注册、Execution 配置和本模块内 SO Loader。
- Task Coroutine Pool 内部协程、队列和 Function 执行实现。

### 1.6 术语

| 术语 | 含义 |
|---|---|
| App | 由 app.json 描述的一组 Function、Bucket、Dependency 和 Trigger 配置 |
| functions[] | app.json 内嵌 Function 配置，描述 so_name、func_name 和 Resource |
| Registry | 模块内部唯一的运行时配置与索引 |
| so_name | 动态库文件名，作为下游 Pool 识别执行载体的标识 |
| func_name | 共享库内的函数名；与 so_name 组成函数标识 |
| sourceFuncName | 本次数据的生产函数标识，包含 so_name 和 func_name，用于 DIRECT 匹配 |
| DIRECT Dependency | 来源 (so_name, func_name) 到目标函数标识 的独立依赖关系 |
| Bucket | 全局唯一的数据逻辑命名空间 |
| DataReadyEvent | 最小通知，包含 bucket、key、session_id |
| TaskArgument | 一项函数输入，包含 bucket、session_id、key、data_addr |
| TaskInvocation | 提交给 Task Coroutine Pool 的一次函数调用描述 |

## 第 2 章 Pheromone 源码参考与裁剪

V0.6 复用 Pheromone 的 DIRECT 独立依赖、Bucket 到 Trigger 列表、Immediate / ByName 匹配、Task 参数随函数调用消息传递等语义。

本项目裁剪为单节点、单进程：
- 不实现 Coordinator、节点级调度、跨节点数据位置同步和远程传输。
- 不复制 INLINE / SHARED_MEMORY 双载体与 DataRef 生命周期；统一传递 data_addr。
- Function 配置通过 app.json 内嵌 functions[] 解析；资源信息随 TaskInvocation 传给 Pool。
- Pool 只提供 TryEnqueue，不增加静态注册、unregister 或 ctx 接口。
- 本模块用 Go 实现；下游 Function Loader 的具体技术不在本模块范围。

## 第 3 章 总体软件架构

### 3.1 核心模块架构

~~~mermaid
flowchart TB
    subgraph Upstream["上游"]
      SDK["Data Bucket SDK"]
      INIT["TriggerInit(app.json)"]
    end
    subgraph PM["Pheromone-mini（Go）"]
      CFG["JSON Parser / Config Loader"]
      REG["App Registry / Function Index / Bucket Index"]
      DIR["DIRECT Dependency"]
      EVT["Event Trigger"]
      TB["Task Builder"]
      RT["Task Router"]
      RES["Resource Probe"]
      DQ["Delay Queue"]
    end
    subgraph Downstream["下游"]
      POOL["Task Coroutine Pool"]
      FN["Function"]
    end
    INIT --> CFG --> REG
    SDK -->|"EventTriggerNotify"| EVT
    REG --> DIR
    REG --> EVT
    DIR --> TB
    EVT --> TB
    TB --> RT
    RT --> RES
    RT -->|"TryEnqueue(TaskInvocation)"| POOL
    RT --> DQ
    DQ --> RT
    POOL --> FN
    FN -->|"Object API / 来源函数标识"| SDK
~~~

### 3.2 关键控制流与数据流

1. 启动时由上游调用 TriggerInit(appJSONPath)，模块读取 app.json，构建内部 Registry。
2. Data SDK 完成 Object Commit 后调用 EventTriggerNotify。
3. Event Trigger 根据 bucket 定位 App；DIRECT 优先匹配来源标识 (so_name, func_name)。
4. Task Builder 以当前 data_addr 和 key 构建 Args[]。
5. Task Router 使用 FunctionInfo.Resource 检查 CPU / Memory。
6. 资源满足时调用 TryEnqueue；Pool 负责队列消费和协程调度。
7. 资源不足、探测 / 计算失败不调用 Pool；与 Pool NoCapacity 一样，记录原因后按延时调度配置进入 Delay Queue 或终止本次路由。

### 3.3 架构原则

- 配置解析只发生在 TriggerInit 阶段；运行期使用只读索引。
- DIRECT 和 Bucket Trigger 是两条独立路径。
- DataReadyEvent 保持最小；数据地址和来源函数标识作为独立参数。
- FunctionInfo 随 TaskInvocation 传递，不设计第二套静态注册协议。
- Pool TryEnqueue 是唯一的下游交互，不传 ctx。
- 数据地址只借用，不复制、不释放；由上游 Session 生命周期兜底。

---

<a id="part-2"></a>

## 第二部分：核心模块详细设计


本部分描述 TriggerInit / JSON Parser、App Registry、DIRECT Dependency、Event Trigger、Task Builder 和 Task Router 的职责、数据结构、处理流程和验收规则。需求 ID 以第六部分为唯一来源；小节可同时覆盖多个明确列出的 ID。所有运行期模块使用同一个内部 Registry；不向上游暴露 Registry 生命周期接口。

## 第 4 章 核心模块总结构

### 4.1 模块划分

| 核心模块 | 内部功能点 | 输入 | 输出 |
|---|---|---|---|
| TriggerInit / Config Loader | 读取 app.json、解析内嵌 functions[]、字段校验 | appJSONPath | 候选 App 配置 |
| App Registry | 索引构建、全局唯一性和只读查询 | 候选 App 配置 | App / Function / Bucket / Dependency / Trigger 索引 |
| DIRECT Dependency | 来源函数查询、目标函数展开、DIRECT 优先级 | (so_name, func_name) + 当前 App | 一个或多个 DIRECT TaskAction |
| Event Trigger | Bucket 查询、Immediate、ByName、多规则匹配 | DataReadyEvent | 一个或多个 Trigger TaskAction |
| Task Builder | 设置任务 InputMode、Args[] 构建、异步元数据复制 | TaskAction + 事件数据视图 | TaskInvocation |
| Task Router | CPU / Memory 预检查、原子准入、Delay Queue、失败日志 | TaskInvocation | Pool 接收或 Router 持有的延时任务 |

### 4.2 模块依赖

~~~mermaid
flowchart LR
    A["TriggerInit / Config Loader"] --> B["App Registry"]
    B --> C["DIRECT Dependency"]
    B --> D["Event Trigger"]
    C --> E["Task Builder"]
    D --> E
    B --> E
    E --> F["Task Router"]
    F --> G["Task Coroutine Pool：TryEnqueue"]
    F --> H["Delay Queue"]
~~~

App Registry 在启动阶段构建只读索引。运行期 DIRECT Dependency 和 Event Trigger 只查询索引，不修改配置。Task Builder 不决定路由；Task Router 不重新解析 Trigger。

### 4.3 统一运行时对象

- DataReadyEvent：上游通知视图，仅包含 bucket、key、session_id。
- EventDataView：EventTriggerNotify 的独立数据参数，包含 sourceFuncName（FunctionIdentity）、data_addr。
- TaskAction：本模块内部对象，包含目标 FunctionInfo 和 InputMode；不传给下游。
- FunctionInfo：so_name、func_name 和 ResourceSpec。
- TaskArgument：一项函数输入，包含 bucket、session_id、key、data_addr。
- TaskInvocation：交给 Pool 的一次函数调用描述，包含 FunctionInfo、InputMode 和 Args[]。
- RouterPendingTask：Task Router 私有对象，包含 TaskInvocation 的自有元数据、首次延时时间和下次重试时间；data_addr 仍为借用地址，不拥有数据内存。

## 第 5 章 TriggerInit / App Registry / JSON Parser

### 5.1 功能点 APP-01：TriggerInit 加载 app.json

对外入口：

~~~go
func TriggerInit(appJSONPath string) error
~~~

职责：读取用户自行编写的 app.json，解析 App 级配置及内嵌 functions[]。JSON Parser 是内部组件，不单独对上游开放。

处理步骤：

1. 检查 appJSONPath 非空并读取 app.json。
2. 解析 App 顶层配置：app_name、version、scheduler、functions、buckets、dependencies、triggers。
3. 解析 functions[] 中每项内嵌的 Function 字段。
4. 形成每个 Function 的完整 FunctionRecord。
5. 校验字段类型、必填字段、唯一性及跨对象引用。
6. 在临时对象中构建 Function、Bucket、DIRECT、Trigger 索引。
7. 全部成功后将该 App 原子合并到内部 Registry；失败时不产生部分可见配置。

每个 app.json 描述一个 App。单进程需要加载多个 App 时，可在运行事件开始前多次调用 TriggerInit；所有初始化调用完成后再开放业务事件。事件处理开始后不再接受新配置。

### 5.2 功能点 APP-03：Bucket 全局唯一索引

Bucket 在当前 Registry 管理的全部 App 之间全局唯一。

~~~text
bucket_index:
    bucket_name -> AppRecord + TriggerList
~~~

收到事件后先根据 bucket 找到所属 App。DataReadyEvent 不携带 app_name。

### 5.3 功能点 APP-04 / APP-05：Function 双标识与 Resource

app.json 的 functions[] 每项描述一个 Function，核心结构：

~~~json
{
  "so_name": "image_process.so",
  "func_name": "crop_image",
  "description": "裁剪图像",
  "version": "1.0.0",
  "resource": {
    "cpu_cores": 2,
    "memory_mb": 512,
    "gpu": {
      "required": false,
      "device_count": 0,
      "memory_mb": 0
    },
    "disk_mb": 0,
    "network_mbps": 0
  }
}
~~~

| 字段 | 含义 | 使用方 |
|---|---|---|
| so_name | SO 文件名 | TaskInvocation 透传给 Pool |
| func_name | Function 标识 | DIRECT 匹配、Trigger 目标定位，并透传给 Pool |
| resource | 该 Function 的静态资源需求 | Task Router 资源预检查 |
| description / version | Function 描述和版本信息 | Registry 元数据 |

规则：

1. so_name 和 func_name 是不同概念，不能合并成 handler。
2. 一个 SO 可以对应多个 func_name。
3. (so_name, func_name) 在当前 App 内唯一，不同共享库可以使用同名函数。
4. DIRECT 和 Trigger 的函数引用可使用包含 so_name 和 func_name 的对象。函数名在当前 App 内只匹配一个函数时，也可使用字符串简写。
5. 完整 resource 绑定到当前 Function 记录，即绑定到 so_name + func_name 这一对标识；Task Router 必查 CPUCores 和 MemoryMB，其余声明的资源按工程实现设计 §14.7 检查，完整配置透传给 Pool。
6. 同一 so_name + func_name 被多个 App 复用时，其 resource 必须一致。
7. so_name 必须是安全文件名，不允许路径穿越。
8. func_name 必须非空。

### 5.4 功能点 APP-06：Function、DIRECT 和 Trigger 引用校验

- functions[] 每项必须同时配置 so_name 和 func_name。
- 同一 App 内 (so_name, func_name) 不得重复。
- DIRECT 的 src_functions / tgt_functions 必须引用当前 App 已注册的函数。
- Trigger 的目标 function 必须引用当前 App 已注册的函数。
- Bucket 必须在当前 App 的 buckets 中声明。
- resource.cpu_cores、resource.memory_mb 必须是有效正数；gpu.device_count、gpu.memory_mb、disk_mb、network_mbps 必须是非负数。
- 整数字段无损解析并校验 Go 类型范围；内存及时间换算上限见附录 B §B.5。多 App 延时配置不一致时拒绝当前 App，不能覆盖已有全局配置。
- 本模块不加载 SO、不执行 dlsym；不在初始化阶段要求下游 Pool 注册 Function。

### 5.5 功能点 APP-07：只读索引发布

~~~text
function_index[app_name][(so_name, func_name)] -> FunctionInfo {
    so_name,
    func_name,
    resource
}

direct_index[app_name][(so_name, func_name)] -> [{目标函数标识, InputMode}]
bucket_index[bucket_name] -> AppRecord + TriggerList
~~~

初始化构建过程在临时对象中完成。只有当前 app.json 的全部配置解析、校验、索引构建均成功后，才将 AppRecord 原子发布到内部 Registry。配置失败不影响已经成功加载的其他 App。

### 5.6 功能点 APP-02：内嵌 Function 配置

functions[] 直接包含 Function 配置，不接受外部配置路径；字段规则和示例见附录 B。

初始化与通知顺序（强制约束）：

- 用户先按附录 B 自行编写 app.json，再串行调用 TriggerInit；所有预期 App 的初始化均成功返回后，才允许发送 EventTriggerNotify。初始化与通知不得并发。
- 内部状态为 Uninitialized、Initializing、Ready、Running。首次初始化失败回到 Uninitialized；追加 App 初始化失败保留已有索引并回到 Ready，调用方须修复失败配置后再开放事件。
- 未初始化或仍在初始化时收到通知，返回 ErrNotInitialized，不匹配、不构建、不提交任务。
- Ready 中首个通过参数和 Bucket / 来源函数校验的通知将状态切换为 Running；非法通知不触发切换。Running 只读，不接受后续 TriggerInit；该调用返回带原因的初始化错误。
- 调用方负责确认全部 App 已初始化，无需新增 seal API。

## 第 6 章 DIRECT Dependency

### 6.1 功能点 DIR-01：独立依赖索引

DIRECT 不作为 Trigger Primitive，而是独立依赖路径。

~~~json
{
  "type": "DIRECT",
  "src_functions": ["image_reader"],
  "tgt_functions": ["image_processor", "image_analyzer"]
}
~~~

~~~text
direct_index[app_name][(so_name, func_name)] -> [{目标函数标识, InputMode}]
~~~

Bucket 全局索引先确定所属 App。初始化时，模块从当前 App 的 functions[] 解析 src_functions 引用的共享库和函数名，以 (so_name, func_name) 建立 DIRECT 索引。

### 6.2 功能点 DIR-02 / DIR-03 / DIR-04：DIRECT 优先级和目标展开

1. 来源为 FunctionIdentity{}：跳过 DIRECT，进入 Bucket Trigger。
2. 来源为非零值：校验两个字段都有效，且精确标识当前 App 中的同一个 Function，否则返回 ErrInvalidArgument。
3. 存在 DIRECT 目标函数标识列表：为每个目标函数生成一个 DIRECT TaskAction，本次事件不再执行 Bucket Trigger。
4. 没有 DIRECT 目标：进入 Bucket Trigger 匹配。
5. 一个来源对应多个目标时，每个目标生成独立 TaskInvocation。

### 6.3 功能点 DIR-05：DIRECT 输入规则

每条 DIRECT 依赖可选 `trigger_option`，整数 `0`（默认，Value）、`1`（Key + Value）、`2`（Key Only）。同一条依赖的所有目标使用同一模式，需要不同模式时拆成多条依赖。同一 App 内重复的 DIRECT 来源／目标对在初始化时拒绝。 参数始终保留原始 Bucket、SessionID、Key 和 DataAddr，Pool 依据 InputMode 生成执行输入视图。此配置不改变来源匹配条件或 DIRECT 优先级。Task Builder 使用当前事件的 bucket、session_id、key 和通知的 data_addr 构建一个 TaskArgument，每个任务的 Args 长度为 1。

## 第 7 章 Event Trigger

### 7.1 功能点 EVT-01 / EVT-02：全局 Bucket 定位

Event Trigger 根据 DataReadyEvent.Bucket 查询 bucket_index，取得所属 App 和该 Bucket 的有序 Trigger[]。

### 7.2 功能点 EVT-03：Immediate 匹配

Immediate 表示该 Bucket 收到有效数据就绪事件时命中；每条匹配规则独立生成 TaskAction。

### 7.3 功能点 EVT-04：ByName 精确匹配

ByName 表示 Bucket 和事件 Key 同时匹配时命中：

~~~text
event.Bucket == trigger.Bucket
AND
event.Key == trigger.KeyName
~~~

### 7.4 功能点 EVT-05：同 Bucket 多 Trigger 独立处理

一个 Bucket 可以关联多条 Trigger。事件到达后逐条匹配；每条命中的 Trigger 单独生成 TaskAction；每个目标任务单独构建 TaskInvocation。多个任务可以共享同一 data_addr。

### 7.5 功能点 EVT-06：DIRECT 与 Bucket Trigger 路径隔离

- 来源标识存在 DIRECT targets：只执行 DIRECT。
- 来源为 FunctionIdentity{}或没有 DIRECT targets：执行 Bucket Trigger。
- 不把 DIRECT 配置写成 Immediate / ByName Trigger。
- 不在同一事件上同时执行 DIRECT 和 Bucket Trigger。

## 第 8 章 Task Builder

### 8.1 功能点 TSK-01：输入模式转换

| trigger_option | InputMode | Function 输入语义 |
|---:|---|---|
| 0 | InputValue | Value |
| 1 | InputKeyValue | Key + Value |
| 2 | InputKeyOnly | Key Only |

DIRECT 按依赖项的 trigger_option 映射输入模式，缺省为 InputValue。TaskArgument 内部始终包含必填 Bucket、SessionID、Key 和 DataAddr。

### 8.2 功能点 TSK-02：构建 Args[]

~~~go
type TaskArgument struct {
    Bucket    string
    SessionID string
    Key       string
    DataAddr  unsafe.Pointer
}
~~~

当前一次 EventTriggerNotify 对应一个数据对象：

~~~text
len(Args) = 1
Args[0].Bucket = event.Bucket
Args[0].SessionID = event.SessionID
Args[0].Key = event.Key
Args[0].DataAddr = dataAddr
~~~

一个事件命中多个目标函数时，每个目标分别获得自己的 TaskInvocation 和参数描述符，但 DataAddr 指向同一份上游数据。

### 8.3 功能点 TSK-03 / TSK-04：TaskInvocation 组装

~~~go
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

type TaskInvocation struct {
    Function  FunctionInfo
    InputMode InputMode
    Args      []TaskArgument
}
~~~

Task Builder 必须复制异步任务需要持有的字符串，不能保留 notify 调用方的临时字符串引用。DataAddr 是借用地址，不复制、不释放。

### 8.4 功能点 TSK-05：每个命中目标独立构建

- 一个 DIRECT source 对应多个 target：每个 target 一个 TaskInvocation。
- 一个 Bucket 多条 Trigger 命中：每条 Trigger 一个 TaskInvocation。
- 每个 TaskInvocation 拥有自己的描述符和字符串元数据。
- 多个 TaskInvocation 可以共享同一 DataAddr。

## 第 9 章 Task Router

### 9.1 功能点 RTE-01：Function 资源需求读取

Task Router 使用 TaskInvocation.Function.Resource 获取该目标 Function 的静态资源需求。FunctionInfo 来自内部 Registry，不允许由上游事件伪造。

### 9.2 功能点 RTE-02：CPU / Memory 可用资源预检查

1. 首次及每次延时重试必须获取当前进程所在资源域的 CPU 与 Memory 限额和使用量快照，不得跳过。
2. 优先通过 /proc/self/cgroup 定位当前进程所属 cgroup。
3. cgroup v2 读取 cpu.max、cpu.stat、memory.max、memory.current。
4. cgroup v1 读取对应 CPU quota / period、CPU accounting 和 memory limit / usage 文件。
5. /proc/meminfo 提供主机内存约束；不得在 cgroup 读取失败时用主机值绕过容器限制。
6. 根据可用 CPU / Memory 与 FunctionInfo.Resource 判断是否满足准入条件。

资源查询是快照，不是资源预留；Pool 的 TryEnqueue 仍然负责队列容量和原子入队。

### 9.3 功能点 RTE-03：资源不足分支

CPU 或 Memory 不足时记录 RESOURCE_INSUFFICIENT；探测、样本解析或准入计算失败时记录 RESOURCE_PROBE_FAILED。两者均不调用 TryEnqueue，打印原因日志并判断 scheduler.delay_queue.enabled；启用则进入 / 保留 Delay Queue，未启用则终止本次路由。后续再扩展进程间 / 节点间调度上报。

### 9.4 功能点 RTE-04 / RTE-05 / RTE-06：原子 TryEnqueue

~~~go
type TaskPool interface {
    TryEnqueue(task TaskInvocation) EnqueueResult
}
~~~

返回值 Accepted / NoCapacity / Error 与接收、描述符复制边界按第三部分 §10.5.7 和 §10.7 执行。

TryEnqueue 必须原子完成队列容量判断和入队；接口不传 ctx，Pool 实例自身持有队列和运行状态。

### 9.5 功能点 RTE-07 / RTE-08：全局 Delay Queue

Delay Queue 是进程级全局配置。资源不足、探测失败、计算失败或 Pool NoCapacity 均判断是否启用延时调度：启用则暂存重试，未启用则记录日志并终止；Pool Error 仍直接终止。

首次遇到上述可延时原因时固定 deadline，原因变化或重试均不重置；延时重试必须先判断原始 deadline，再返回资源检查；禁止直接重试 TryEnqueue。计算和采样规则见第四部分 §14.2。

### 9.6 功能点 RTE-09：Pool 消费与协程唤醒边界

Task Router 只提交 Task，不调用单独的协程唤醒接口。Task Coroutine Pool 保证成功入队的 Task 最终可被内部消费者发现。

---

<a id="part-3"></a>

## 第三部分：接口与协作设计


> 本文定义 V0.6 Go API、数据类型和上下游协作契约。上游只调用 TriggerInit 和 EventTriggerNotify；下游只提供 TryEnqueue(TaskInvocation)。

## 第 10 章 接口范围与公共类型

### 10.1 参与方

| 参与方 | 职责 | 与本模块的接口关系 |
|---|---|---|
| 上游 SMC Actor / Data Bucket SDK | 写入并 Commit 数据，提供数据地址，维护 Session 生命周期 | 启动阶段调用 TriggerInit；运行阶段调用 EventTriggerNotify |
| Pheromone-mini（Go） | 配置加载、Registry、DIRECT、Event Trigger、Task Builder、Task Router、Delay Queue | 提供两个上游 API；调用下游 TryEnqueue |
| Task Coroutine Pool | 原子任务入队、队列消费、协程调度、SO / Function 加载和执行 | 只提供 TryEnqueue(TaskInvocation) |
| Function / Object SDK | 处理 TaskContext 输入，通过 Object API 写出数据 | 输出事件时注入当前 so_name 和 func_name |

### 10.2 接口方向

~~~mermaid
flowchart LR
    APP["上游应用启动流程"] -->|"TriggerInit(appJSONPath)"| PM["Pheromone-mini（Go）"]
    SDK["上游 Data Bucket SDK"] -->|"EventTriggerNotify(event, sourceFuncName, dataAddr)"| PM
    PM -->|"TryEnqueue(TaskInvocation)"| POOL["Task Coroutine Pool"]
    POOL -->|"按 SOName + FuncName 加载并执行"| FN["Function"]
    FN -->|"Object API Commit；SDK 注入 sourceFuncName"| SDK
~~~

### 10.3 接口分组与边界

- A 组：本模块提供给上游的 API，共两个：TriggerInit、EventTriggerNotify。
- B 组：下游 Task Coroutine Pool 唯一接口：TryEnqueue(TaskInvocation)。
- 不存在 Pool 静态 Function 注册接口，不存在 register_function_configs。
- 不存在 DataRef Adapter、validate、retain、release 等 Data SDK 回调。
- FunctionInfo（so_name、func_name、resource）随 TaskInvocation 传递。
- Pool TryEnqueue 不带 ctx；Pool 实例自身持有队列和运行状态。

### 10.4 API 清单

#### 10.4.1 A 组：本模块提供给上游的 API

| API ID | API 名称 | 调用方 | 调用阶段 | 功能 |
|---|---|---|---|---|
| PM-API-001 | TriggerInit | 上游应用启动流程 | 启动阶段 | 传入 app.json 路径；模块读取 app.json（包含内嵌 Function 配置），完成解析、校验及 Registry 初始化 |
| PM-API-002 | EventTriggerNotify | Data Bucket SDK / Object SDK | 每次数据 Commit 成功后 | 提交 Bucket、Key、Session、来源函数和数据地址，触发 DIRECT / Bucket Trigger 判断并路由任务 |

API 编号按当前接口从 001 连续重排；不沿用历史编号。除以上两个 API 外，不暴露 Registry 生命周期、单独 JSON Parser 或索引操作接口。

#### 10.4.2 B 组：下游 Task Coroutine Pool API

| API ID | API 名称 | 调用方 | 功能 |
|---|---|---|---|
| POOL-API-001 | TryEnqueue | Task Router | 原子检查队列容量并提交完整 TaskInvocation |

TryEnqueue 是下游唯一交互接口。不提供 register_function、unregister_function、wake_one 或 DataRef 操作。

### 10.5 公共类型与返回语义

#### 10.5.1 DataReadyEvent

~~~go
type DataReadyEvent struct {
    Bucket    string
    Key       string
    SessionID string
}
~~~

DataReadyEvent 仅包含 Bucket、Key、SessionID，不包含 sourceFuncName、dataAddr、payload_type、DataRef、app_name、event_id、trigger_name 或目标函数。

#### 10.5.2 TaskArgument

~~~go
type TaskArgument struct {
    Bucket    string
    SessionID string
    Key       string
    DataAddr  unsafe.Pointer
}
~~~

Bucket、SessionID 和 Key 必须存在；DataAddr 必须非空。一次通知对应一个对象，每个派生任务的 Args 长度为 1。

#### 10.5.3 InputMode

~~~go
type InputMode uint8

const (
    InputValue InputMode = iota
    InputKeyValue
    InputKeyOnly
)
~~~

| 值 | 来源 | Function 输入语义 |
|---:|---|---|
| InputValue | `trigger_option = 0` 或省略（同样适用于 DIRECT 和 Bucket Trigger） | Value |
| InputKeyValue | trigger_option=1 | Key + Value |
| InputKeyOnly | trigger_option=2 | Key Only |

`trigger_option` 是 `app.json` 中 DIRECT 依赖项和 Bucket Trigger 的可选整数配置，省略时按 `0` 处理。解析配置时，`0`、`1`、`2` 分别映射为 Go 类型 `InputMode` 的 `InputValue`、`InputKeyValue`、`InputKeyOnly`，运行时写入 `TaskInvocation.InputMode`。

#### 10.5.4 ResourceSpec 与 FunctionInfo

~~~go
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
~~~

SOName 与 FuncName 作用不同。Resource 保存 Function 的 CPU、Memory、GPU、Disk、Network 静态需求，并随 TaskInvocation 一起传给 Pool；Task Router 本期必须根据 CPUCores 和 MemoryMB 做准入检查。当前不定义 Execution 字段。

#### 10.5.5 TaskInvocation

~~~go
type TaskInvocation struct {
    Function  FunctionInfo
    InputMode InputMode
    Args      []TaskArgument
}
~~~

Bucket、SessionID、Key 和 DataAddr 一起保存在 Args 的输入对象描述中。TaskInvocation 不包含 AppName、sourceFuncName、TriggerName、DataRef 或固定 handler 字段。

#### 10.5.6 上游 API 统一错误格式

两个上游 API 统一返回 Go `error`：成功为 `nil`，失败为非空 `*APIError`。失败对象只定义两个公开字段：

~~~go
type StatusCode uint16

const (
    ErrNotInitialized  StatusCode = 1001
    ErrInvalidArgument StatusCode = 1002
    ErrConfigRead      StatusCode = 1003
    ErrInvalidConfig   StatusCode = 1004
    ErrInvalidState    StatusCode = 1005
    ErrInternal        StatusCode = 1500
)

type APIError struct {
    StatusCode StatusCode `json:"status_code"`
    Msg        string     `json:"msg"`
}

func (e *APIError) Error() string {
    return fmt.Sprintf("%d: %s", e.StatusCode, e.Msg)
}
~~~

`Msg` 为非空诊断文本，可包含文件路径、字段路径或失败原因，但不包含业务数据或敏感配置值。调用方用 `errors.As` 取得 `*APIError`，按 `StatusCode` 分支；不得解析 `Error()` 或匹配 `Msg`。上述 `Err*` 是状态码常量。状态码含义保持稳定，未知码按一般失败处理；成功不构造错误对象。

#### 10.5.7 Pool 入队返回值

~~~go
type EnqueueResult uint8

const (
    EnqueueAccepted EnqueueResult = iota
    EnqueueNoCapacity
    EnqueueError
)
~~~

| 返回值 | 语义 | Router 行为 |
|---|---|---|
| EnqueueAccepted | Pool 已原子入队并接收任务描述符 | Router 释放自己的描述符副本 |
| EnqueueNoCapacity | Pool 当前队列无可接收容量，未接管任务 | 按 Delay Queue 配置暂存或终止 |
| EnqueueError | Pool 内部错误，未接管任务 | 终止任务并记录内部错误 |

### 10.6 A 组：本模块对上游 API 规格

#### 10.6.1 PM-API-001：TriggerInit

~~~go
func TriggerInit(appJSONPath string) error
~~~

职责：根据 app.json 文件路径加载 App 配置及内嵌 functions[]，完成解析、校验、索引构建并发布内部 Registry。

返回类型：`error`。成功返回 `nil`；失败返回 §10.5.6 定义的 `*APIError`。

| 返回结果 | 含义 | 调用方处理 |
|---|---|---|
| `nil` | 当前 App 的配置已校验并原子发布 | 继续初始化其他 App；全部成功后才发送事件 |
| `ErrInvalidArgument` (1002) | 路径为空或无法规范化 | 修正路径后在启动阶段重试 |
| `ErrConfigRead` (1003) | 配置文件不存在、不可读或读取失败 | 修正路径或权限后在启动阶段重试 |
| `ErrInvalidConfig` (1004) | JSON 格式非法、字段或引用非法，或与已加载配置冲突 | 根据文件路径、字段路径及原因修正配置 |
| `ErrInvalidState` (1005) | 模块已进入 Running，不再接受初始化 | 不在当前运行周期重试 |
| `ErrInternal` (1500) | 无法构建索引或发布配置，且原因不属于输入配置错误 | 记录底层原因，停止开放事件并排查 |

任何失败都不发布当前 App 的部分配置，也不修改已成功发布的其他 App 配置。调用方按 `StatusCode` 判断，`Msg` 用于诊断。

路径和配置规则：

1. appJSONPath 不能为空；读取前转换为规范化绝对路径。
2. 用户按附录 B 自行编写 app.json，模块只读取、解析和校验，不生成或补写业务配置。
3. functions[] 内嵌每个 Function 的 so_name、func_name、resource 等字段；不读取额外函数配置文件。

初始化处理顺序：

1. 读取 app.json。
2. 解析 App 顶层配置及内嵌 functions[]。
3. 从 functions[] 逐项解析 SOName、FuncName、Resource。
4. 校验 App、Function、Bucket、DIRECT、Trigger 和 Delay Queue 配置，包括数值换算边界及与已加载 App 的延时配置一致性（附录 B）。
5. 校验 DIRECT / Trigger 的函数引用均能唯一解析到当前 App 中的函数。
6. 在临时对象中构建 Function、Bucket、DIRECT、Trigger 索引。
7. 全部成功后原子发布当前 AppRecord；任何一步失败，不留下部分可见配置。

每个 app.json 描述一个 App。单进程加载多个 App 时，可在业务事件开始前多次调用 TriggerInit；所有初始化调用完成后再开放业务事件。运行期开始后不再接受新配置。

#### 10.6.2 PM-API-002：EventTriggerNotify

~~~go
type FunctionIdentity struct {
    SOName   string `json:"so_name"`
    FuncName string `json:"func_name"`
}

func EventTriggerNotify(
    event DataReadyEvent,
    sourceFuncName FunctionIdentity,
    dataAddr unsafe.Pointer,
) error
~~~

入参：

| 参数 | 类型 | 必须 | 说明 |
|---|---|---:|---|
| event | DataReadyEvent | 是 | 仅 Bucket、Key、SessionID |
| sourceFuncName | FunctionIdentity | 是，可为零值 | 当前生产函数的 SOName 和 FuncName，由协程池绑定并由 SDK 透传，见 §10.8.2。外部 SMC Actor 传 FunctionIdentity{} |
| dataAddr | unsafe.Pointer | 是，非空 | 已 Commit 对象的非空地址，空载荷对象也须提供有效地址 |

处理顺序：

1. 检查初始化状态。未完成初始化时返回 ErrNotInitialized。随后校验 Bucket、Key、SessionID 和 dataAddr，地址必须非空。
2. 根据 Bucket 查询全局 Bucket Index，定位所属 App。
3. sourceFuncName 为非零值时，校验 SOName 和 FuncName 均有效且精确匹配该 App 中的同一函数，否则返回 ErrInvalidArgument。
4. 查询 DIRECT Dependency Index。
5. 存在 DIRECT 目标时，为每个目标构建独立任务。按该依赖的 trigger_option 选择 Value（0，缺省）、Key + Value（1）或 Key Only（2），跳过本次事件的 Bucket Trigger。
6. 无 DIRECT 目标时，按 Bucket 匹配 `IMMEDIATE` 或 `BY_NAME` 规则。
7. 每条命中规则独立构建 TaskInvocation；每个任务的 Args 长度为 1。
8. Task Router 必查 CPU / Memory，并按声明检查 GPU / Disk / Network。
9. 资源不足、探测或准入计算失败时不调用 TryEnqueue；分别记录 RESOURCE_INSUFFICIENT 或 RESOURCE_PROBE_FAILED 日志（reason 区分探测 / 计算），判断延时调度是否启用：启用则暂存重试，未启用则终止本次路由。
10. 资源满足时调用 TryEnqueue；NoCapacity 按 Delay Queue 规则处理。

延时调度为模块内部异步处理；资源失败和 Pool NoCapacity 共用现有配置，不新增 API。任务首次因任一可延时原因等待时建立 deadline，后续重试和原因切换不重置；到期优先终止。

返回类型：`error`。失败统一返回 `*APIError`；以下 `Err*` 为 `StatusCode`。

| 返回值 | 含义 |
|---|---|
| `nil` | 通知已按规则处理。无命中、直接入队、进入延时队列或按路由策略终止个别任务，都不构成通知 API 错误；不代表所有目标已入队或执行完成 |
| `ErrNotInitialized` (1001) | 模块尚未完成初始化或正在初始化；不匹配、不路由 |
| `ErrInvalidArgument` (1002) | Bucket、Key 或 SessionID 为空，Bucket 未注册，来源标识不完整或与该 App 的函数定义不匹配，或数据地址为空；不匹配、不路由 |
| `ErrInternal` (1500) | 处理通知时遇到不可恢复的模块内部错误，例如 Registry 内部不变量被破坏；不保证已经提交的任务会回滚，调用方不能据此直接重发通知 |

资源不足、探测或计算失败、Pool 拒绝、延时队列已满或到期均按任务路由规则处理。尤其是 Pool 返回 `EnqueueError` 时终止该任务并记日志，不直接映射为通知 API 的 `ErrInternal`。

### 10.7 B 组：下游 Task Coroutine Pool API 规格

#### 10.7.1 POOL-API-001：TryEnqueue

~~~go
type TaskPool interface {
    TryEnqueue(task TaskInvocation) EnqueueResult
}
~~~

返回类型：`EnqueueResult`（底层类型 `uint8`，定义见 §10.5.7），不另返回 Go `error`。

| 枚举值 | 数值 | Pool 是否接管 | 含义与 Router 行为 |
|---|---:|---|---|
| `EnqueueAccepted` | 0 | 是 | 任务已原子入队，Pool 已复制异步执行所需的描述符；Router 清理自有元数据，不重试。业务数据地址仍为借用 |
| `EnqueueNoCapacity` | 1 | 否 | 队列当前无容量，任务未入队；启用延时且未到期、暂存有容量时暂存并重试，否则终止该任务 |
| `EnqueueError` | 2 | 否 | Pool 无法正常提交任务，任务未入队；Router 记录内部错误并终止该任务，不进入延时队列 |

该接口不带 ctx 参数。Go 方法接收者 / Pool 实例自身持有队列、协程池和运行状态；不需要额外 ctx。

Pool 实例创建、装配和生命周期由下游 / 集成方负责，不属于本模块设计或验收范围。本模块仅按 POOL-API-001 调用下游 API，不新增 SetPool、RegisterPool 或第三个上游 API。

#### 10.7.2 TryEnqueue 调用语义

1. TryEnqueue 必须原子完成容量判断和入队。
2. Accepted 表示 Pool 已复制 TaskInvocation 的 Args 切片及其后续执行需要独立持有的描述符；Go string 可安全共享其不可变内容。
3. Pool 不复制、不释放 DataAddr 指向的业务数据。
4. NoCapacity 表示未接管任务；Pool 不得在返回后继续引用调用方可变 Args 切片。
5. Error 表示未接管任务。
6. Pool 成功接收后，必须保证任务最终能被内部消费者发现。
7. Pool 不要求本模块额外调用 wake_one。
8. TryEnqueue 不等待容量恢复或任务执行完成，也不在调用栈内同步执行业务函数；无容量立即返回 NoCapacity。不得在持有 Router / Delay Queue 全局锁时调用下游。

#### 10.7.3 Pool 消费与唤醒责任

- Pool 负责队列消费者、协程创建、等待、唤醒和任务完成。
- Pool 根据 TaskInvocation.Function.SOName 和 FuncName 识别执行目标。
- Pool 按自己的实现选择 Function 加载和符号定位机制；本模块不规定 Go Plugin、cgo 或 native SO Loader。
- Pool 不负责释放 DataAddr 指向的上游数据内存。

### 10.8 Function 执行上下文契约

#### 10.8.1 逻辑 TaskContext

~~~go
type TaskContext struct {
    SOName    string
    FuncName  string
    InputMode InputMode
    Args      []TaskArgument
}
~~~

TaskContext 是 Pool 与 Function / Object SDK 的逻辑执行上下文，不是本模块第三个上游 API。具体函数加载和调用 ABI 由下游 Pool 决定。

#### 10.8.2 Object API 与来源函数注入

`sourceFuncName` 表示产出这份数据的已注册函数，来自本次执行任务的 `TaskInvocation.Function` 的 SOName 和 FuncName。协程池在调用函数前，将这两个字段绑定到本次调用的 TaskContext。函数通过 Object API 写出中间结果，Object SDK 在数据 Commit 成功后从当前调用上下文取值并透传给 `EventTriggerNotify`。SDK 无需解析 app.json 或生成函数名称，业务函数也无需自行填写来源。并发执行时，每次调用使用自己的上下文。

外部 SMC Actor 写入首份数据时没有池内任务上下文，SDK 传 FunctionIdentity{}。调度器根据 Bucket 找到所属 App。来源为零值时直接评估 Bucket Trigger。来源为非零值时先验证它属于该 App，再查该函数的 DIRECT 依赖。命中时为配置中的所有目标分别构建任务并跳过本次 Bucket 规则，未命中时继续评估 Bucket Trigger。

例如，Actor 写入 `raw` Bucket，SDK 传空来源，由 Immediate 规则拉起 `decode`。`decode` 写出中间结果时，SDK 透传协程池绑定的共享库和函数名；若 App 配置了 `decode → infer` 的 DIRECT 依赖，调度器便将该数据地址交给 `infer` 对应的任务。

## 第 11 章 上下游协作流程与生命周期

### 11.1 启动期

~~~go
if err := TriggerInit("./config/app.json"); err != nil {
    return err
}
~~~

用户先按附录 B 编写 app.json；模块读取该文件并解析内嵌 functions[]。上游等待全部 TriggerInit 成功返回后才可发送事件，不调用 Registry 注册接口。

### 11.2 运行期：上游通知一次 Commit

~~~go
event := DataReadyEvent{
    Bucket:    "b_input",
    Key:       "image_001",
    SessionID: "session_1001",
}

err := EventTriggerNotify(
    event,
    FunctionIdentity{},
    committedObjectAddr,
)
~~~

Function 输出对象时，第二个参数传入由当前 TaskContext 的 SOName 和 FuncName 组成的标识；外部 Actor 传 FunctionIdentity{}。

### 11.3 运行期：Pool 接收任务

~~~text
TaskInvocation
    Function.SOName = image_process.so
    Function.FuncName = crop_image
    Function.Resource = {CPUCores: 2, MemoryMB: 512}
    InputMode = InputValue
    Args[0]:
        Bucket = b_input
        SessionID = session_1001
        Key = image_001
        DataAddr = committedObjectAddr
~~~

Task Router 先检查资源，再调用 TryEnqueue。Pool 自行根据 SOName + FuncName 加载并执行目标函数。

### 11.4 端到端时序

~~~mermaid
sequenceDiagram
    participant App as Application Bootstrap
    participant SDK as Data Bucket SDK
    participant ET as Event Trigger
    participant TB as Task Builder
    participant RT as Task Router
    participant Pool as Task Coroutine Pool
    participant Fn as Function

    App->>ET: TriggerInit(appJSONPath)
    ET->>ET: 读取 app.json，构建 Registry
    ET-->>App: 初始化成功后才允许发送事件
    SDK->>ET: EventTriggerNotify(event, sourceFuncName, dataAddr)
    ET->>ET: Bucket 定位 / DIRECT 判断 / Trigger 匹配
    ET->>TB: TaskAction + EventDataView
    TB->>TB: 构建 TaskInvocation.Args[]
    TB->>RT: TaskInvocation
    RT->>RT: 查询当前可用 CPU / Memory
    alt 资源不足、探测或计算失败
        RT->>RT: 记录原因；不调用 Pool
        alt Delay enabled
            RT->>RT: 暂存 / 保留任务，固定 deadline，截止前按间隔重试资源检查
        else Delay disabled
            RT->>RT: 终止本次路由
        end
    else 资源满足
        RT->>Pool: TryEnqueue(TaskInvocation)
        alt Accepted
            Pool->>Pool: 入队并由内部协程消费
            Pool->>Fn: 根据 SOName + FuncName 加载并执行
        else NoCapacity
            RT->>RT: Delay Queue 或终止
        else Error
            RT->>RT: 记录内部错误并终止
        end
    end
~~~

### 11.5 内存所有权与生命周期契约

| 对象 | 创建方 | 使用方 | 释放方 / 有效期 |
|---|---|---|---|
| DataReadyEvent | 上游 SDK | EventTriggerNotify | 上游保证 notify 调用期间有效 |
| sourceFuncName | 上游 SDK / TaskContext | DIRECT 匹配 | 上游保证 notify 调用期间有效 |
| Bucket / Key / SessionID 字符串 | 上游 SDK | Task Builder / Pool / Function | Task Builder 复制异步任务所需字符串 |
| TaskInvocation / Args 描述符 | Task Builder | Router / Pool | Router 或 Pool 管理自己的描述符副本 |
| DataAddr 指向的数据 | 上游 Data SDK | Task Builder / Pool / Function | Data SDK 自行判定完成并回收；本模块不提供完成判定契约 |

不可违反的规则：

1. DataAddr 是借用地址，不发生所有权转移。
2. Scheduler 和 Pool 不得释放 DataAddr。
3. Task Builder 不复制数据内容，只构建参数描述符。
4. 一个事件命中多个 Task 时，所有 Task 都可能访问同一 DataAddr。
5. Data SDK 保证 Delay Queue 和异步执行期间借用地址有效。
6. Session 完成判定及数据回收由 Data SDK 管控，本模块不定义完成通知、引用计数或回收协议。
7. DataAddr 应指向上游管理的共享内存 / mmap 区域，不应指向未固定的 Go Heap 对象。


### 11.6 并发、重入与调用顺序

初始化与通知顺序（强制约束）：

- 用户先按附录 B 自行编写 app.json，再串行调用 TriggerInit；所有预期 App 的初始化均成功返回后，才允许发送 EventTriggerNotify。初始化与通知不得并发。
- 内部状态为 Uninitialized、Initializing、Ready、Running。首次初始化失败回到 Uninitialized；追加 App 初始化失败保留已有索引并回到 Ready，调用方须修复失败配置后再开放事件。
- 未初始化或仍在初始化时收到通知，返回 ErrNotInitialized，不匹配、不构建、不提交任务。
- Ready 中首个通过参数和 Bucket / 来源函数校验的通知将状态切换为 Running；非法通知不触发切换。Running 只读，不接受后续 TriggerInit；该调用返回带原因的初始化错误。
- 调用方负责确认全部 App 已初始化，无需新增 seal API。

- TriggerInit 仅在启动阶段调用；所有配置初始化完成后再开始业务事件。
- Registry 发布后只读。
- EventTriggerNotify 可被多个上游线程并发调用。
- Task Builder 不得把调用方的临时字符串引用直接放入异步队列。
- Task Router 和 Delay Queue 必须线程安全。
- TryEnqueue 必须原子完成队列准入和入队。
- 同一数据地址被多个任务共享时，上游负责保证并发读期间地址稳定。
- 模块关闭前停止接收新通知，等待在途通知和 Router PendingTask 进入终态；Pool 已接收任务按 Pool 的关闭契约完成或终止。

### 11.7 上下游责任边界与联调验收

#### 11.7.1 本模块负责

- TriggerInit(appJSONPath)：读取 app.json（包含内嵌 Function 配置），完成解析、校验和 Registry 初始化。
- EventTriggerNotify：最小事件、来源函数和数据地址通知。
- Function / Bucket / DIRECT / Trigger 索引。
- Task Builder.Args[]。
- CPU / Memory 资源预检查。
- TryEnqueue 调用与 Delay Queue。

#### 11.7.2 上游 Data Bucket SDK 负责

- Commit 成功后调用 EventTriggerNotify。
- 传递 Bucket、Key、SessionID、sourceFuncName、dataAddr。
- 保证数据地址在 Session 相关任务完成或终止前有效。
- Session 结束后回收数据。
- Function 输出对象时注入当前 FuncName。

#### 11.7.3 下游 Task Coroutine Pool 负责

- 运行期提供唯一 TryEnqueue(task TaskInvocation)。
- 不要求 ctx 参数或静态 Function 注册。
- 原子入队、队列消费和协程唤醒。
- 根据 SOName + FuncName 加载并执行 Function。
- 复制任务描述符和 Args 切片，不复制、不释放上游数据内容。

#### 11.7.4 联调验收清单

- 上游只使用 TriggerInit 和 EventTriggerNotify 两个 API。
- TriggerInit 只接收 app.json 路径，并解析内嵌 functions[]。
- DataReadyEvent 只有 Bucket、Key、SessionID。
- sourceFuncName 是独立参数，外部 Actor 使用 FunctionIdentity{}。
- dataAddr 与上游 Commit 对象一致。
- 不存在 DataRef Adapter 或 DataRef 相关 API。
- 当前 App 内的 (so_name, func_name) 组合唯一，DIRECT 按该组合匹配来源。
- TaskInvocation 携带 FunctionInfo 和 Args[]，每个任务的 Args 长度为 1。
- 下游 Pool 只有 TryEnqueue，不带 ctx。
- 不存在 Pool 静态 Function 注册 API。
- Task Router 在 TryEnqueue 前执行 CPU / Memory 资源预检查。
- 资源不足、探测或计算失败不调用 TryEnqueue；启用延时则暂存重试，未启用则记录日志并终止。
- Pool NoCapacity 与资源不足、探测 / 计算失败保留不同原因；共用延时配置和固定 deadline，Pool Error 不重试。
- Session 生命周期覆盖所有派生任务。

---

<a id="part-4"></a>

## 第四部分：工程实现设计


## 第 12 章 Go 工程结构

### 12.1 仓库目录

~~~text
pheromone-mini/
├── api/        # 对外接口
├── docs/       # 正式设计
├── examples/   # 配置和 Go 对接示例
├── internal/   # 配置、契约、引擎、资源、计时和运行时装配
└── tests/      # 配置、引擎、资源和 Mock 端到端测试
~~~

对上游只导出两个业务函数；internal 下的实现不作为客户 API。

### 12.2 模块依赖规则

- config 只负责读取 app.json、JSON 解析和配置校验。
- registry 依赖 config，负责索引构建和只读查询。
- direct_dependency 与 event_trigger 只读取 Registry 索引。
- task_builder 依赖 TaskAction、事件数据视图和 Function Index，不调用 Data SDK。
- task_router 依赖 TaskInvocation.Function.Resource、Resource Probe、Pool TryEnqueue 和 Delay Queue。
- Resource Probe 不依赖 Trigger 或 Task Builder。
- Pool 内部实现不进入本模块；本模块只依赖 TryEnqueue 契约。

### 12.3 Go 实现规范

- 对外类型使用明确字段名。
- DataAddr 使用 unsafe.Pointer 表示上游共享内存 / mmap 地址；不得将普通 Go Heap 指针作为跨异步任务的借用数据地址，除非上游保证其在整个生命周期内固定。
- 对异步任务需要持有的字符串和 Args 切片进行复制；不复制 DataAddr 指向的数据内容。
- 不把 unsafe.Pointer 转成 uintptr 后长期保存。
- Registry 配置发布后只读；索引查询不修改配置。
- 错误使用 Go error；EventTriggerNotify 对外只区分未初始化、参数非法和内部错误三类。
- 使用 gofmt；测试执行 go test ./...、go vet ./... 和 go test -race ./...。
- 本模块不实现 Go Plugin、cgo 或 native SO Loader；so_name / func_name 作为 Function 标识传递给下游。

## 第 13 章 并发、生命周期与可靠性

### 13.1 内部 Registry 发布

1. 启动时调用 TriggerInit(appJSONPath)。
2. 读取用户编写的 app.json，解析内嵌 functions[]。
3. 在临时对象中完成 JSON 解析、字段校验和配置引用校验。
4. 建立 Function、Bucket、DIRECT、Trigger 索引。
5. 在 Registry 写锁保护下，将当前 AppRecord 原子合并到内部 Registry。
6. 任一步骤失败则当前 App 初始化失败，不留下部分可见配置。

Registry 是单进程单实例，不提供外部 create / register / seal / destroy API。多个 App 必须在业务事件开始前完成 TriggerInit。

初始化与通知顺序（强制约束）：

- 用户先按附录 B 自行编写 app.json，再串行调用 TriggerInit；所有预期 App 的初始化均成功返回后，才允许发送 EventTriggerNotify。初始化与通知不得并发。
- 内部状态为 Uninitialized、Initializing、Ready、Running。首次初始化失败回到 Uninitialized；追加 App 初始化失败保留已有索引并回到 Ready，调用方须修复失败配置后再开放事件。
- 未初始化或仍在初始化时收到通知，返回 ErrNotInitialized，不匹配、不构建、不提交任务。
- Ready 中首个通过参数和 Bucket / 来源函数校验的通知将状态切换为 Running；非法通知不触发切换。Running 只读，不接受后续 TriggerInit；该调用返回带原因的初始化错误。
- 调用方负责确认全部 App 已初始化，无需新增 seal API。

### 13.2 Function 与索引唯一性

- (so_name, func_name) 在当前 App 内唯一。
- so_name 可以被同一 App 的多个 Function 共享。
- Function 记录以 so_name + func_name 作为执行入口组合标识。
- FunctionInfo.Resource 与对应 Function 记录绑定。
- DIRECT 索引以当前 App + (so_name, func_name) 为键，目标为函数标识列表。
- Bucket 在当前 Registry 所有 App 之间全局唯一。
- Trigger 目标解析为 (so_name, func_name)。

### 13.3 EventTriggerNotify 并发

DataReadyEvent 和 sourceFuncName 是只读借用值，在调用期间有效。Task Builder 必须复制后续异步任务所需的 Bucket、Key、SessionID 等字符串。

DataAddr 不复制、不释放。上游必须保证数据地址在 Session 结束且该 Session 产生的所有任务（包括 Delay Queue 项）完成或终止之前有效。

多个 Task 可能同时读取同一 DataAddr；上游保证数据在此期间不可变或至少满足并发读取安全要求。

### 13.4 TaskInvocation 元数据生命周期

- Task Builder 构建阶段：创建任务描述符和字符串副本。
- Task Router 持有阶段：Router 拥有任务描述符；DataAddr 仍为借用。
- TryEnqueue = Accepted：Pool 在返回前复制后续执行所需的 TaskInvocation / Args 描述符；Router 可释放自己的元数据副本。
- TryEnqueue = NoCapacity / Error：Pool 不保留调用方可变 Args 切片引用；Router 继续持有或终止任务。
- Delay Queue：Router 持有元数据副本，并继续借用 DataAddr。
- 任务完成或终止：Pool / Router 释放各自拥有的元数据；数据内存由上游按 Session 规则回收。

Go string 不可变，可以共享其底层字节；Args 是 slice，跨异步边界必须复制 slice 本身及其元素描述符。

### 13.5 Session 数据生命周期责任边界

Data SDK 全权负责 Session 生命周期、任务完成判定及数据回收。本模块仅借用地址，不维护 Session 任务计数，不新增派生 / 终态登记、完成回调、回收协议或 DataRef。Data SDK 保证异步排队和执行期间地址有效；其内部完成判定机制不作为本模块设计冻结或验收前置项。

## 第 14 章 Task Router 与 Delay Queue 实现

### 14.1 资源预检查原则

1. 从 TaskInvocation.Function.Resource 读取该 Function 的静态资源需求。
2. 获取当前进程可见的 CPU / Memory 限额与使用量。
3. 比较 CPUCores、MemoryMB 及§14.7 中任务已声明的资源；所有要求维度都通过才提交。完整需求随 FunctionInfo 传给 Pool。
4. 不满足时记录 RESOURCE_INSUFFICIENT；探测 / 解析 / 计算失败时记录 RESOURCE_PROBE_FAILED。两者均不调用 TryEnqueue，打印原因日志并判断 scheduler.delay_queue.enabled；启用则进入 / 保留 Delay Queue，未启用则终止本次路由。
5. 满足时调用 Pool TryEnqueue。

延时参数在初始化时无损解析并检查 time.Duration 换算上限；每个追加 App 的延时配置必须与已加载配置一致。静态内存需求换算溢出也在初始化时拒绝，运行时样本异常仍按资源计算失败处置。具体数值见附录 B §B.5。

资源预检查是 P0 必做项：首次提交和每次延时重试均执行，不得跳过。这里只做快照判断，不是资源预留。本期资源分支记录原因日志并按现有延时配置处置；不调用外部上报，后续扩展进程间 / 节点间调度。

### 14.2 Linux 资源信息读取

- 通过 /proc/self/cgroup 获取当前进程所属 cgroup，并用 /proc/self/mountinfo 定位当前进程可见的挂载。物理机与虚拟机使用各自 Linux 实例的系统统计；容器还需纳入可见控制组限额。成员文件不存在时，只有 /proc/filesystems 确认内核未提供 cgroup 文件系统才使用系统统计。
- cgroup v2：读取 cpu.max、cpu.stat、memory.max、memory.current。内核层级根没有控制器限额文件；非根组仅在父组未启用对应控制器且成员目录可读时允许缺少限额文件，仍须检查可见祖先。私有命名空间的根也可能显示为 /；该目录提供 cgroup.type 时仍按非根组处理，父组不可见且无法确认限额时返回采集错误。
- cgroup v1：分别定位 CPU、cpuacct、memory 和 cpuset 走各自的挂载；混合层级按控制器选择版本。有限 CPU 配额要求能读取对应层级的 CPU 用量。cpuset 支持旧文件名和 noprefix 挂载。
- /proc/meminfo：读取 MemTotal / MemAvailable。旧内核仅在确实缺少 MemAvailable 时使用 MemFree 作为不计可回收缓存的估计；字段存在但无效时返回采集错误。
- CPU 可用配额与使用量应结合 cgroup CPU quota 和 accounting 计算。

~~~go
type ResourceSnapshot struct {
    CPULimitCores       float64
    CPUUsedCores        float64
    CPUAvailableCores   float64
    MemoryLimitBytes    uint64
    MemoryUsedBytes     uint64
    MemoryAvailableBytes uint64
    SourceIsCgroup      bool
}
~~~

资源快照计算规则：

- CPU 单位为核，内存按 MiB（1 MiB = 1024×1024 字节）；配置 memory_mb 在比较前检查 uint64 乘法溢出。
- 有效 CPU 上限取在线 CPU、进程亲和性 / cpuset、当前及祖先 cgroup 有限 quota/period 中的最小值；无限 quota 不作为限制。内存有效上限取主机总量与当前及祖先 cgroup 有限限额的最小值。
- 每 100 ms 采样 CPU 累计使用时间；UsedCores = ΔCPUTime / ΔMonotonicTime。各约束域分别计算 max(0, LimitCores - UsedCores)，取最小可用值，避免把进程使用量当作整个容器使用量。首次提交尚无双样本时采样一次，100 ms 后取第二次；共享采样器，禁止每个任务独立睡眠。
- 内存可用量取主机 MemAvailable 与各有限 cgroup 的 max(0, limit-usage) 的最小值。完全无 cgroup 的宿主机环境使用主机统计；存在 cgroup 但应读文件失败时不得以主机资源绕过容器限制。
- 快照最大年龄 200 ms；过期须刷新。CPU 集合发生变化、负增量、无效时间窗口、非有限数、读取 / 解析失败或单位换算溢出均视为探测失败；记录 RESOURCE_PROBE_FAILED 和具体 reason，按延时配置暂存重试或终止；不使用零值或无限值放行。
- CPUAvailableCores >= CPUCores 且 MemoryAvailableBytes >= MemoryMB×1024×1024 才满足准入；边界相等允许。日志包含需求、可用量、采样时间、来源和失败原因；不输出业务数据。

例：有效 CPU 上限 4 核，100 ms 内使用 150 ms CPU 时间，则可用 2.5 核；内存限额 1024 MiB、使用 768 MiB、主机 MemAvailable 4096 MiB，则可用 256 MiB。需求 2 核 / 256 MiB 通过，需求 3 核或 512 MiB 不通过并记录日志。

### 14.3 资源不足与队列无容量分离

| 状态 | 发生阶段 | 是否调用 TryEnqueue | 处理 |
|---|---|---:|---|
| CPU / Memory 资源不足 | Pool 入队前 | 否 | RESOURCE_INSUFFICIENT 日志；启用延时则暂存重试，否则终止 |
| 资源探测 / 准入计算失败 | Pool 入队前 | 否 | RESOURCE_PROBE_FAILED 日志；启用延时则暂存重试，否则终止 |
| Pool NoCapacity | Pool 原子入队时 | 是 | 按 Delay Queue 策略暂存或终止 |
| Pool Error | Pool 原子入队时 | 是 | 记录错误并终止 |

资源不足、探测失败、计算失败和 Pool NoCapacity 共用 scheduler.delay_queue.enabled。启用时交给同一 Delay Queue，未启用时记录日志并终止；资源类失败不调用 Pool，Pool Error 不进入 Delay Queue。日志保留各自原因，不将探测 / 计算失败混同为资源不足。

### 14.4 Delay Queue 数据结构

~~~go
type RouterPendingTask struct {
    Task         TaskInvocation
    FirstDeferred time.Time
    NextRetry    time.Time
    Deadline     time.Time
}
~~~

Router PendingTask 拥有 TaskInvocation 的描述符和 Args 切片副本，但不拥有 DataAddr 指向的数据内存。

### 14.5 延时调度状态机

~~~mermaid
stateDiagram-v2
    [*] --> ResourceCheck
    ResourceCheck --> DelayDecision: 资源不足、探测或计算失败；记录原因
    ResourceCheck --> TryEnqueue: 资源满足且未过 deadline
    TryEnqueue --> PoolOwned: Accepted
    TryEnqueue --> DelayDecision: NoCapacity；记录原因
    TryEnqueue --> Failed: Error；记录错误
    DelayDecision --> DelayPending: enabled 且未过期且有暂存容量
    DelayDecision --> Rejected: disabled 或暂存队列满
    DelayDecision --> Expired: deadline 已到
    DelayPending --> ResourceCheck: 到达重试时间且未过期
    DelayPending --> Expired: deadline 已到
    PoolOwned --> [*]
    Rejected --> [*]
    Failed --> [*]
    Expired --> [*]
~~~

统一规则：

1. 首次资源不足、探测失败、计算失败或 Pool NoCapacity 时，先记录原因，再判断 enabled。未启用则终止，不创建延时项。
2. 启用时，在首次可延时原因发生时设置 FirstDeferred 和 deadline = FirstDeferred + max_wait_ms。这个时间起点不再只依赖 Pool NoCapacity。
3. 暂存 TaskInvocation 及元数据，不复制、不释放数据地址；同一任务最多一个待重试项。Delay Queue 满则记录拒绝原因并释放 Router 自有元数据，不做忙循环。
4. 每次唤醒先检查单调时钟 deadline；到期优先记录 TASK_DELAY_EXPIRED 并终止。未到期则重新探测和计算 CPU / Memory；只有资源满足才能调用 TryEnqueue。
5. 复查仍资源不足、探测或计算失败时，仍按 enabled 判断；启用则保留同一待重试任务，下一次重试时间为 min(本次尝试结束时间 + retry_interval_ms, deadline)。不重置 FirstDeferred / deadline，不新增重复延时项。
6. 资源检查可能耗时，在资源检查返回后、调用 TryEnqueue 前再次检查 deadline。即使资源恢复，只要已到期就不得提交。首次尚未创建 deadline 的任务不作过期判断；deadline 从首次可延时原因开始。
7. Pool NoCapacity 的每次重试同样按 min(本次尝试结束时间 + retry_interval_ms, deadline) 安排下一次唤醒，不允许立即循环重试。原因可以在资源不足、探测失败、计算失败和 NoCapacity 之间变化，但共用首次固定 deadline；已 Accepted 的任务不再重试，Pool Error 直接终止。
8. 日志记录 delay_reason、probe_failure_reason、delay_enabled、delay_state、首次延时时间和 deadline；探测 / 计算失败仍使用 RESOURCE_PROBE_FAILED，并通过 reason 区分。仅记录日志，不做进程间 / 节点间调度上报。

### 14.6 退出清理

模块关闭时停止接收新事件；Router 停止接受新 Task；Delay Queue 中未提交任务按关闭策略取消并释放 Router 自有元数据；Pool 已接收任务由 Pool 按其关闭契约清理。任何一方都不得释放上游 DataAddr。

### 14.7 GPU、磁盘和网络准入

CPU / Memory 总是检查。GPU 声明 required=true 或任一 GPU 数值非零时检查（device_count=0 时按 1）；disk_mb、network_mbps 大于零时检查对应维度。新增可选顶层 scheduler.resource_probe；有磁盘需求必须填写 disk_path（已存在的绝对路径），有网络需求必须填写 network_interface、network_limit_mbps（有限正整数，部署方提供的有效带宽上限）。多个 App 可指定不同资源域，快照按资源域隔离，不混用。

| 维度 | 计算规则 |
|---|---|
| GPU | 从部署提供的设备遥测读取当前进程可见设备的稳定 ID 与可用显存；按 ID 去重。至少 device_count 个设备分别满足每卡 memory_mb（MiB），不跨卡加总。遥测适配器为模块内部依赖，不新增 Pool API |
| 磁盘 | Linux statfs(disk_path) 的可用块数 Bavail × 块大小，换算后比较 disk_mb（MiB）；路径必须落在任务在当前挂载命名空间使用的文件系统。文件系统未报告 Frsize 时采用 Bsize。仅反映可读到的文件系统可用量，不承诺用户配额或预留 |
| 网络 | 在当前网络命名空间按配置网卡读取 RX/TX 字节累计计数，使用共享单调采样窗口；按十进制 Mbps，分别计算两个方向速率，使用两者最大值。可用量 = max(0, network_limit_mbps − max(RX Mbps, TX Mbps))，同需求比较 |

所有要求维度都满足才调用 TryEnqueue；相等通过。无请求维度不探测，无需 GPU 设备或网卡才能运行旧配置。按 §14.2 的共享采样与有效期处理，首次及每次重试重新判断；不可读、采样无效、计数回退或溢出记录 RESOURCE_PROBE_FAILED，可靠样本不足记录 RESOURCE_INSUFFICIENT（即样本有效、容量不够）。两者都沿用 enabled 和固定 deadline。缺静态配置、单位换算越界由 TriggerInit 拒绝；没有可用遥测不得按充足放行。准入不分配 GPU、不锁定磁盘或网络，也不改变 Pool 的执行权。

## 第 15 章 错误处理、日志与性能

### 15.1 错误分层

- API 参数错误：返回 ErrInvalidArgument。
- 尚未初始化：返回 ErrNotInitialized。
- JSON / Registry 初始化错误：阻止当前 App 进入运行态，返回带文件路径和字段路径的 error。
- 资源不足：不调用 TryEnqueue，记录 RESOURCE_INSUFFICIENT；按 enabled 暂存重试或终止。
- 资源探测 / 计算失败：不调用 TryEnqueue，记录 RESOURCE_PROBE_FAILED 及具体 reason；按 enabled 暂存重试或终止。两类分支本期均不做外部上报。
- Pool 队列无容量：根据 Delay Queue 策略处理。
- Pool 内部错误：记录并终止当前任务。
- 无 Trigger 命中：正常处理，不作为错误。

### 15.2 结构化日志字段

建议字段：trace_seq、app_name、bucket、key、session_id、source_so_name、source_func_name、so_name、func_name、route_path、trigger_name、input_mode、args_count、resource_check、delay_reason、probe_failure_reason、delay_enabled、deadline、cpu_available、memory_available、pool_result、delay_state、elapsed_ms、dispatched。内部诊断字段不进入公共 API。日志字段 `input_mode` 记录运行时 `TaskInvocation.InputMode`。

### 15.3 性能原则

- JSON 只在 TriggerInit 阶段解析。
- Registry 索引运行期只读。
- Event Trigger 只按 Bucket 索引查找候选规则。
- DIRECT 按来源的 (so_name, func_name) 索引查找目标。
- Task Builder 只构建参数描述符，不复制数据内容。
- 资源查询尽量使用轻量快照，避免每个事件执行高开销完整扫描。
- TryEnqueue 必须一次调用完成原子队列准入和入队。

## 第 16 章 构建、测试与交付

### 16.1 构建目标

- Go Module。
- JSON Parser / Registry 单元测试。
- Event Trigger / DIRECT 单元测试。
- Task Builder / Task Router 单元测试。
- Mock TaskPool 自检、本模块内部链路测试和 Mock SDK → 真实调度模块 → Fake Pool → 测试函数输出回流的端到端测试。
- 内部依赖可替换：资源样本 / 文件读取、单调时钟、定时器、日志采集；不新增客户 API。

### 16.2 构建要求

- Linux 环境。
- Go 工具链版本由项目构建环境统一锁定。
- 使用 gofmt。
- Resource Probe 依赖 Linux procfs / cgroup 文件接口。
- CI 至少执行 go test ./...、go vet ./... 和 go test -race ./...；并增加配置 / 事件模糊测试、Mock 端到端及资源原始样本 / 目标环境检查。测试设施就绪后执行，不能把设计要求当成已运行结果。

### 16.3 实现顺序

1. JSON Parser、app.json 配置校验和内部 Registry。
2. Event Trigger：Immediate / ByName / DIRECT。
3. Task Builder：Args[] 和元数据复制。
4. Task Router：CPU / Memory 预检查、TryEnqueue、Delay Queue。
5. 实现测试事件发生器、Fake Pool 自检、资源 / 时钟夹具；完成单元及内部链路验收。
6. 增加 Mock SDK 输出回流、异步测试函数和最终结果断言，完成 Mock 端到端验收。
7. 真实 SDK / Pool 就绪后进入联合集成验收；不等待联调才开始独立测试。

本期优先完成 JSON Parser、Event Trigger、Task Router 三个模块；Task Builder 作为轻量内部组件随链路实现。

### 16.4 测试可替换性与验收边界

完整测试设施、场景和通过门槛见[附录 E](#part-11)。测试替身在内部组合入口装配，不将 Fake Pool 的创建 / 注册方法作为生产 API，不扩大本模块对真实 Pool 实例装配的职责。Mock E2E 运行真实 Registry、规则、Task Builder、Router 和 Delay Queue。

单例 Registry 的客户 API 链路用例默认进程隔离；独立私有实例用于单元测试。测试时钟须覆盖采样、重试和 deadline，使用同步屏障验证异步执行，不靠固定 sleep。Fake Pool Accepted 后复制描述符并异步消费，测试只借用稳定 mmap 数据，夹具收尾不变成 Data SDK 的生产回收契约。

---

<a id="part-5"></a>

## 第五部分：需求追踪与测试验收


> 当前为测试设计，未代表测试执行通过。各模块并行开发期间先完成本模块独立验收和 Mock 端到端验收，真实上下游联调单独记录。具体测试环境、脚本约束和场景见[附录 E](#part-11)。

## 第 17 章 功能需求与验收矩阵

### 17.1 需求追踪

| 需求组 | 核心能力 | 设计章节 | 验收范围 |
|---|---|---|---|
| APP | TriggerInit、app.json 解析、索引 | 5 | TEST-APP |
| DIR | DIRECT 独立依赖、来源函数和目标展开 | 6 | TEST-DIR |
| EVT | Immediate / ByName、多 Trigger、路径优先级 | 7 | TEST-EVT |
| TSK | InputMode、Args[]、TaskInvocation | 8 | TEST-TSK |
| RTE | CPU / Memory 预检查、TryEnqueue、Delay Queue | 9、14 | TEST-RTE |
| API | 两个上游 API、Pool TryEnqueue、FunctionInfo 契约 | 03 | TEST-API |

功能需求 ID 以第六部分为唯一来源。详细设计小节允许覆盖多个 ID，测试编号不与需求编号强行一一对应。

| 需求 ID | 测试追踪 |
|---|---|
| APP-01 / APP-02 | TEST-APP-01–04、15–16、TEST-API-02、12–15 |
| APP-03 | Bucket 跨 App 重名拒绝及事件唯一定位（TEST-APP-14） |
| APP-04 / APP-05 | TEST-APP-05–09、11–13 |
| APP-06 / APP-07 | TEST-APP-10、TEST-EVT-05、TEST-DIR-05 |
| DIR-01–05 | TEST-DIR-01–09、TEST-API-04–05 |
| EVT-01–07 | TEST-EVT-01–07、TEST-API-01、03、06、12、TEST-TSK-09 |
| TSK-01–05 | TEST-TSK-01–09 |
| RTE-01–09 | TEST-RTE-01–27、TEST-API-07–11 |
| API-01–05 | TEST-API-01–16、§18.3（SDK 内部回收机制除外） |
| OBS-01 / SAFE-01–03 | TEST-RTE-20、TEST-APP-10–11、§18.3 |

跨阶段追踪：APP / DIR / EVT / TSK / RTE / API 在 A 阶段使用本表检查，在 B 阶段分别由附录 E 的 MOCK-E2E-01–20 覆盖完整链路；OBS / SAFE 结合原因日志、非法输入、内存借用和 FUZZ 场景验收。ENV-01–02、CON-01、FUZZ-01–02、REG-01 记录环境与专项证据。C 阶段补充真实上下游接入结果，不覆盖掉 A / B 的独立结论。

资源准入需求 RTE-10–13 对应 TEST-RTE-28–31（§18.5）。

### 17.2 验收原则

1. 单元测试覆盖 app.json 解析、索引、DIRECT、Immediate、ByName、Task Builder 和 Task Router。
2. 参数测试覆盖 Key 必填、DataAddr 非空及地址透传、Args 长度和 FunctionInfo。
3. 并发测试覆盖本模块的通知、共享数据地址、多目标任务、提交返回处理和 Delay Queue。Fake Pool 的原子入队仅自检替身，真实 Pool 的原子入队由下游及联调验证。
4. 资源测试覆盖 CPU / Memory 充足、不足、探测 / 计算失败及 cgroup v1 / v2；不得在 cgroup 读取失败时用主机值绕过限制。原始样本与真实 Linux 环境分别验证。
5. 独立验收用测试事件发生器和 Fake Pool；Mock 端到端增加异步测试函数执行及输出事件回流。真实 Data SDK Commit、真实 Pool TryEnqueue 和 FunctionInfo 透传放在后续联调阶段。
6. 不执行 DataRef 引用计数、Inline 复制、Shared-Memory retain 等已删除设计的测试。
7. 测试代码使用 Go；不保留 C11 / C ABI 示例作为 V0.6 实现契约。
8. 替换 SDK、Pool、资源输入与时钟，不替换本模块的配置、匹配、任务构建、路由或延时逻辑；预期结果独立于实现给出。
9. 测试必须断言具体任务、次数、输入和输出；不能只检查返回 nil 或日志存在。尚未联调标为未执行，不阻塞独立验收，但不得宣称真实集成通过。

## 第 18 章 测试与验收方案

### 18.1 测试分层

| 层次 | 被测真实实现 | 依赖方式 | 阶段 |
|---|---|---|---|
| 单元测试 | 配置解析、Registry、DIRECT、Trigger、Task Builder、Router、Delay Queue | 文件夹具、可控资源与时钟、Fake Pool | A：本模块独立验收 |
| 本模块完整链路 | TriggerInit → EventTriggerNotify → 规则 → Task → 资源 → 提交 / 延时 | 上游事件发生器、Fake Pool；不依赖真实 SDK / Pool | A：本模块独立验收 |
| Mock 端到端 | 完整调度链路，异步执行测试函数，输出经 Mock SDK 回流，核对最终结果 | Mock SDK、Fake Pool、测试函数及可控资源 / 时钟 | B：Mock 端到端验收 |
| Linux 环境 | 真实资源文件解析 / 计算和目标环境适配器 | procfs / cgroup 原始样本与真实环境分别运行 | A 的环境验证 |
| 并发、模糊与回归 | 并发通知、延时与消费交错、退出清理、非法输入 | race、屏障和回执、固定种子及保留失败语料 | A、B 的质量门槛 |
| 真实集成 | 真实 Commit、任务入队与实际函数执行链路 | 真实 SDK 和 Pool | C：真实上下游联调 |

A、B 可在上下游开发期间独立执行。不同层次的状态、缺陷和证据分别记录；目录、测试设施及详细场景见附录 E。

### 18.2 核心测试用例

#### TriggerInit / JSON Parser

| 用例 | 输入 / 条件 | 预期结果 |
|---|---|---|
| TEST-APP-01 | 用户编写的合法 app.json，functions[] 内嵌配置完整 | 初始化成功并发布只读 Registry |
| TEST-APP-02 | app.json JSON 语法错误 | 初始化失败，不发布当前 App |
| TEST-APP-03 | app.json 文件缺失或不可读 | 初始化失败，不发布当前 App |
| TEST-APP-04 | functions[] 项结构 / 类型错误 | 初始化失败，不发布当前 App |
| TEST-APP-05 | Function 缺少 so_name | 初始化失败 |
| TEST-APP-06 | Function 缺少 func_name | 初始化失败 |
| TEST-APP-07 | 同一 App 内 (so_name, func_name) 重复 | 拒绝配置 |
| TEST-APP-08 | 同一 SO 对应多个不同 func_name | 配置通过 |
| TEST-APP-09 | 不同 App 存在同名 func_name | 允许；按 Bucket 所属 App 隔离 |
| TEST-APP-10 | Trigger / DIRECT 引用不存在的 func_name | 初始化失败 |
| TEST-APP-11 | so_name 包含路径穿越 | 拒绝配置 |
| TEST-APP-12 | resource.cpu_cores 或 memory_mb 非法 | 初始化失败 |
| TEST-APP-13 | gpu.device_count、gpu.memory_mb、disk_mb 或 network_mbps 为负数 | 初始化失败 |
| TEST-APP-14 | 不同 App 使用同一 Bucket | 拒绝后一个 App，不损坏已发布索引 |
| TEST-APP-15 | 时间 / 内存换算上限及 uint32 / uint64 范围边界、超界、小数 | 合法边界无损解析；超界、小数或换算溢出在初始化时拒绝 |
| TEST-APP-16 | 多 App 的 delay_queue 任一有效配置不一致 | 拒绝当前 App，保留已有 Registry 和全局延时配置 |

#### DIRECT Dependency

| 用例 | 输入 / 条件 | 预期结果 |
|---|---|---|
| TEST-DIR-01 | 来源标识有一个 DIRECT 目标 | 生成一个 DIRECT Task |
| TEST-DIR-02 | 来源标识有多个 DIRECT 目标 | 每个目标生成独立 Task |
| TEST-DIR-03 | 来源标识无 DIRECT 映射 | 进入 Bucket Trigger |
| TEST-DIR-04 | 来源为 FunctionIdentity{} | 进入 Bucket Trigger |
| TEST-DIR-05 | 来源标识不完整、库名不匹配或在当前 App 不存在 | 参数非法，不误匹配其他 App |
| TEST-DIR-06 | DIRECT 目标函数 | TaskInvocation 携带 SOName + FuncName + Resource |
| TEST-DIR-07 | DIRECT 命中且 Bucket 配置 Trigger | 本次事件只走 DIRECT |
| TEST-DIR-08 | DIRECT 分别省略选项、设置 0 / 1 / 2，以及同来源不同目标分别配置模式 | 校验每个目标的 InputMode、Args 和实际测试函数输入视图；Key Only 不读取 Value；无 Bucket Trigger 调用 |
| TEST-DIR-09 | DIRECT 选项为 null、非整数或越界，或重复来源／目标对 | 初始化失败，不发布该 App |

#### Event Trigger

| 用例 | 输入 / 条件 | 预期结果 |
|---|---|---|
| TEST-EVT-01 | Immediate Bucket 收到事件 | 命中 |
| TEST-EVT-02 | ByName Bucket + Key 完全匹配 | 命中 |
| TEST-EVT-03 | ByName Key 不匹配 | 不命中 |
| TEST-EVT-04 | 同 Bucket 两条 Trigger 均命中 | 生成两个独立 Task |
| TEST-EVT-05 | Bucket 不存在 | 返回 ErrInvalidArgument，不跨 App 匹配 |
| TEST-EVT-06 | event.Key 为空 | 返回 ErrInvalidArgument |
| TEST-EVT-07 | 未支持的 Bucket Trigger 原语或非 DIRECT 依赖 | TriggerInit 返回 ErrInvalidConfig（1004），不发布该 App |

#### Task Builder

| 用例 | 输入 / 条件 | 预期结果 |
|---|---|---|
| TEST-TSK-01 | trigger_option=0 | InputValue |
| TEST-TSK-02 | trigger_option=1 | InputKeyValue |
| TEST-TSK-03 | trigger_option=2 | InputKeyOnly |
| TEST-TSK-04 | 任意 InputMode | TaskArgument.Key 始终存在 |
| TEST-TSK-05 | 有效数据地址 | Args[0] 保存原始地址，不复制数据 |
| TEST-TSK-06 | 当前基础场景 | Args 长度为 1，Args[0] 正确 |
| TEST-TSK-07 | 多目标命中 | 每个 Task 拥有独立参数描述符，DataAddr 相同 |
| TEST-TSK-08 | EventTriggerNotify 返回后调用方释放元数据引用、延迟消费 | Task 的 Key / SessionID / Args 仍正确；不使用非法 Go 字符串内存模拟失效 |
| TEST-TSK-09 | DataAddr=nil | 参数校验失败 |

#### Task Router / Resource Probe

| 用例 | 输入 / 条件 | 预期结果 |
|---|---|---|
| TEST-RTE-01 | CPU / Memory 均充足 | 调用 TryEnqueue |
| TEST-RTE-02 | CPU 不足；分别启用 / 关闭延时 | 不调用 TryEnqueue，记录 RESOURCE_INSUFFICIENT；启用则暂存重试，否则终止 |
| TEST-RTE-03 | Memory 不足；分别启用 / 关闭延时 | 不调用 TryEnqueue，记录 RESOURCE_INSUFFICIENT；启用则暂存重试，否则终止 |
| TEST-RTE-04 | cgroup v2 | 正确读取 CPU / Memory 限额和使用量 |
| TEST-RTE-05 | cgroup v1 | 正确读取对应限额和使用量 |
| TEST-RTE-06 | 存在 cgroup 但关键文件不可读 | 记录 RESOURCE_PROBE_FAILED，不提交；不得使用主机值绕过限制 |
| TEST-RTE-07 | Pool Accepted | Pool 接收任务描述符 |
| TEST-RTE-08 | Pool NoCapacity + Delay enabled | 进入 Delay Queue，按 retry_interval_ms 重试，不立即循环；deadline 不延长 |
| TEST-RTE-09 | Pool NoCapacity + Delay disabled | 记录拒绝并终止任务 |
| TEST-RTE-10 | Pool Error | 记录内部错误并终止任务 |
| TEST-RTE-11 | Delay 到期 | 记录 TASK_DELAY_EXPIRED |
| TEST-RTE-12 | 探测失败；分别启用 / 关闭延时 | 不调用 TryEnqueue，记录 RESOURCE_PROBE_FAILED 及探测原因；启用则暂存重试，否则终止 |
| TEST-RTE-13 | 本模块并发提交到有界 Fake Pool | 本模块正确处理 Accepted / NoCapacity，无同任务重复接收；Fake Pool 容量自检通过。真实 Pool 原子性留给 C 阶段 |
| TEST-RTE-14 | 启用延时；首次充足且 NoCapacity；重试资源不足 | 不调用第二次 TryEnqueue，记录 RESOURCE_INSUFFICIENT，保留同一延时项及原 deadline |
| TEST-RTE-15 | 启用延时；重试探测 / 计算失败 | 记录 RESOURCE_PROBE_FAILED 及具体 reason，不提交；按间隔继续重试，不重置 deadline |
| TEST-RTE-16 | 重试时间与 deadline 同时到达 | 到期优先，不探测、不提交；不重置 deadline |
| TEST-RTE-17 | 第四部分 §14.2 的 4 核 / 150 ms、1024 MiB / 768 MiB 样本 | 需求 2 核 / 256 MiB 通过；3 核或 512 MiB 不通过 |
| TEST-RTE-18 | 首次无双样本、快照超 200 ms、计数回退或换算溢出 | 首次共享采样，过期刷新；非法样本记录失败日志且不提交 |
| TEST-RTE-19 | 完全无 cgroup 或无限 quota | 按主机 / 亲和性等有效约束计算并记录来源；无限 quota 不设为零 |
| TEST-RTE-20 | 资源不足、探测或计算失败 | 记录日志并按延时配置处置；不调用外部上报或进程间 / 节点间调度 |
| TEST-RTE-21 | 计算失败（零采样时间、计数回退等）；分别启用 / 关闭延时 | 不提交，日志 reason 明确为计算失败；启用则暂存重试，否则终止 |
| TEST-RTE-22 | 首次资源不足；到期前资源恢复、Pool Accepted | 首次不调用 Pool，重试重新检查资源后仅提交一次，移除待重试项 |
| TEST-RTE-23 | 首次探测 / 计算失败，随后恢复且 Pool Accepted | 刷新采样并重新计算后提交，不沿用异常快照 |
| TEST-RTE-24 | 持续资源失败，或原因在资源不足 / 探测失败 / 计算失败 / NoCapacity 间切换 | 共用首次 FirstDeferred 和 deadline；达到 max_wait_ms 后 TASK_DELAY_EXPIRED，停止重试 |
| TEST-RTE-25 | 启用延时，但 Delay Queue 满；重复唤醒同一任务 | 满则记录拒绝并释放 Router 元数据；重复唤醒不产生多份延时项、不重复提交 |
| TEST-RTE-26 | 重试开始未到期，但资源检查完成时已到期 | 提交前复查 deadline，记录到期，不调用 TryEnqueue |
| TEST-RTE-27 | 多目标事件中部分任务资源失败、另一些 Accepted | 各任务独立延时或完成；不重试已 Accepted 任务，不因一个失败中断其他目标 |

#### API / Task Contract

本组既含静态接口 / 范围检查，也含运行时行为检查；报告逐项标明方法，不把“文档没有某接口”写成功能测试已运行。

| 用例 | 输入 / 条件 | 预期结果 |
|---|---|---|
| TEST-API-01 | DataReadyEvent 结构 | 只有 Bucket、Key、SessionID |
| TEST-API-02 | TriggerInit | 只接收 app.json 路径，并解析内嵌 functions[] |
| TEST-API-03 | EventTriggerNotify | 不包含 Registry 参数 |
| TEST-API-04 | sourceFuncName 独立传入 | DIRECT 正确匹配 |
| TEST-API-05 | 外部 Actor 传 FunctionIdentity{} | 正常执行 Bucket Trigger |
| TEST-API-06 | DataAddr | 与上游 Commit 对象一致 |
| TEST-API-07 | TaskPool 接口 | 只有 TryEnqueue(task)，不带 ctx |
| TEST-API-08 | TaskInvocation.Function | 同时包含 SOName、FuncName、Resource |
| TEST-API-09 | Pool 静态注册 | 不存在 register_function_configs |
| TEST-API-10 | Function Loader | 不属于本模块契约，不测试 dlopen / dlsym |
| TEST-API-11 | 资源预检查 | 首次和每次重试均发生在 TryEnqueue 之前 |
| TEST-API-12 | 未初始化 / 初始化未结束时发送事件 | ErrNotInitialized，无匹配、无提交 |
| TEST-API-13 | 多 App 串行初始化，追加 App 失败 | 不发布失败 App；已成功索引不受损；调用方修复后再开放通知 |
| TEST-API-14 | Ready 中非法通知 / 首个合法通知 | 非法通知不切换；合法通知进入 Running |
| TEST-API-15 | Running 中再次 TriggerInit | 返回带原因的初始化错误，Registry 不变 |
| TEST-API-16 | API 清单及规格 | PM-API-001、PM-API-002、POOL-API-001 连续且一致 |

### 18.3 数据地址生命周期验收

| 检查 | A / B 阶段做法 | C 阶段边界 |
|---|---|---|
| 数据地址及字节一致 | 测试夹具用稳定 mmap 内存；核对事件、任务和测试函数收到的地址及字节 | 核对真实 SDK Commit 对象 |
| 多目标和延迟消费 | 延迟 worker 消费并推进延时调度，夹具保持内存有效；各目标读到正确数据 | 验证真实 SDK / Pool 组合下地址有效 |
| 描述符与数据所有权 | Accepted 后仅移除 Router 自有元数据；拒绝 / 超时 / 退出不释放或改写业务数据 | 联调验证真实 Pool 的描述符复制和借用边界 |
| 清理与回收 | 停止事件、清空在途 / pending / worker 后由夹具解除映射；检查内部 goroutine / timer 退出 | Data SDK 内部 Session 完成判定及回收协议由 SDK 团队负责 |

本模块不实现任务完成计数、DataRef 或回收 API；不能把测试夹具的收尾计数变成生产生命周期契约。

### 18.4 DIRECT 与 Trigger 优先级验收

某来源标识同时存在 DIRECT Dependency，且该事件的输出 Bucket 配置了 Immediate / ByName 时，本次事件只走 DIRECT。只有 来源为 FunctionIdentity{}或不存在 DIRECT 映射时，才走 Bucket Trigger。

### 18.5 Mock 端到端验收

用例编号 MOCK-E2E-01–20，完整矩阵见[附录 E §E.6](#e6-mock-端到端场景矩阵)。本组补充前文的编号检查，一项可以展开多组参数。

基本链路：真实初始化 → Mock SDK 写入并通知 → 真实规则 / Router → Fake Pool 异步消费 A → A 的输出经 Mock SDK 通知 → 真实规则触发 B → 核对最终字节结果。

必须覆盖 Immediate / ByName / DIRECT、输入模式、多目标、资源不足 / 探测 / 计算失败的延时开关、队列满、恢复、持续失败超时、原因切换不延长 deadline、提交前再次查超时、队列满拒绝、并发、退出及多 App 隔离。重复通知按不同事件处理，不增加通知去重功能。

Fake Pool 使用真实 TaskPool 类型且完成复制 / 拒绝 / 异步消费自检；TryEnqueue 不等待容量恢复或执行结束；预期目标集合、次数和输出由夹具独立定义。资源和时钟必须可控，不能依赖固定 sleep 及宿主机偶发高负载。

以下用例纳入 A 阶段独立验收；B 阶段运行真实资源准入和路由逻辑，通过可控时钟、样本及 Fake Pool 验证最终输出。GPU、文件系统与网卡再分别在目标 Linux 验证。

| 用例 | 需求 | 场景及断言 |
|---|---|---|
| TEST-RTE-28 | RTE-10 GPU  | 两卡分别满足；总量足但单卡不足；重复设备、不可见设备、无遥测及无需求 |
| TEST-RTE-29 | RTE-11 磁盘  | 有权限可用块、等于需求、路径非法、块乘法溢出及无需求 |
| TEST-RTE-30 | RTE-12 网络  | RX/TX 双向、等于需求、计数回退、网卡不可用、缺上限及无需求 |
| TEST-RTE-31 | RTE-13 多资源联合  | 任一维度不足或探测失败不得提交；启用 / 关闭延时、恢复后提交一次、到期不提交、配置缺失初始化失败 |

### 18.6 质量检查与可重复执行

- Linux 原始样本 / 目标环境、race、并发屏障、模糊测试与回归要求分别见附录 E §E.7–E.8。
- 初始化与全局 Registry 的不同配置用例按独立进程隔离；测试时钟统一覆盖采样与调度，测试钩子仅内部可见，不新增客户 API。
- CI 每个提交运行单元与确定性链路，PR / Linux CI 运行 Mock E2E、race 和对应环境检查；模糊测试保存预算、种子和失败输入。
- 失败用例保留最小复现并纳入回归，禁止靠盲目重跑把失败改成通过。

## 第 19 章 验收准入与交付

### 19.1 功能通过标准

下列行为要求按 A / B / C 分阶段提供证据，不能因为真实 SDK / Pool 尚未完成就跳过本模块可独立执行的验证。

- TriggerInit 能读取 app.json 的全部配置，并原子发布 Registry。
- Function 以 (so_name, func_name) 组合标识，该组合在 App 内唯一。
- DIRECT 按来源的 (so_name, func_name) 查询目标函数。
- Immediate / ByName 和多 Trigger 行为正确。
- DataReadyEvent 仅三个字段，EventTriggerNotify 包含独立 sourceFuncName、dataAddr。
- TaskInvocation.Args[] 正确，Key 必须。
- 不存在 DataRef、INLINE / SHARED_MEMORY 和相关生命周期接口。
- Task Router 在 TryEnqueue 前执行 CPU / Memory 预检查。
- 资源不足、探测或计算失败不调用 TryEnqueue，记录原因后与 Pool NoCapacity 一样按 enabled 暂存重试或终止；Pool Error 不延时。
- 下游 Pool 只提供不带 ctx 的 TryEnqueue。
- 不存在 Pool 静态 Function 注册接口和未经确认的 Execution 配置。
- A / B 中由测试夹具维持有效内存并验收借用边界；C 核对真实组合下的数据有效性，SDK 内部回收不属本模块验收。

### 19.2 分阶段结论和交付物

| 阶段 | 通过门槛 | 交付证据 |
|---|---|---|
| A：本模块独立验收 | P0 需求有已执行测试；单元、内部链路、资源样本、并发 / race、清理和回归通过；目标 Linux 验证单列，无阻塞缺陷 | 固定提交及环境、需求映射、逐项结果、日志、复现输入和未执行项 |
| B：Mock 端到端验收 | 依赖替身自检通过，20 类场景及参数化分支通过；最终结果、任务字段 / 次数、资源和延时状态均有断言 | Mock 场景报告、资源 / Pool 返回脚本、时钟推进、输入输出及异常证据 |
| C：真实联调 | 接口版本一致；真实 SDK / Pool 接入及实际调用链路通过 | 联调环境、双方版本、链路记录和各自负责能力的验证证据 |

环境未覆盖、用例跳过和待联调项目必须标注，不算通过；可以给出已完成阶段的明确结论。覆盖率仅辅助发现遗漏，不代替 P0 行为和关键分支验证。

交付包括本设计及附录 E、app.json 协议和夹具、Go API / Task 契约、可执行单元与 Mock E2E 测试、CI 配置、阶段验收报告。测试设施及报告尚未实现 / 执行时注明状态；本次文档补充不宣称已有测试代码或功能通过。

### 19.3 本期不作为验收目标

- Task Coroutine Pool 的队列、协程、SO Loader 和 Function 执行内部实现。
- Go Plugin、cgo 或 native SO 的技术选型。
- 资源分支的外部上报落地；本期必须验收对应日志。
- Data SDK 内部 Session 完成判定、任务计数和数据回收协议。
- Pool 实例创建、装配和生命周期管理。
- 跨进程、跨节点调度。

### 19.4 报告格式与当前状态

报告至少包含：代码 commit、设计 / 接口版本、Go / Linux / cgroup 环境、命令、用例 ID 和参数、预期 / 实际、日志、种子、失败复现及未执行原因。每项标为未执行、通过、失败或阻塞；静态契约检查与运行时测试分列。

本轮仅完善测试设计，A：本模块独立验收、B：Mock 端到端验收、C：真实上下游联调均未提供执行证据，状态为未执行。后续应以固定提交对应的实际测试报告更新结论，不通过编辑设计文档直接宣布验收通过。

---

<a id="part-6"></a>

## Pheromone-mini V0.6 分功能需求列表


## 1. 文档目的

本文将 V0.6 冻结设计拆解为可实现、可测试、可验收的功能需求。需求以 Pheromone 的 DIRECT、Bucket Trigger 和函数参数传递机制为基础，适配 Go、单进程单调度实例和统一数据地址传递。

## 2. 范围、角色与优先级

### 2.1 角色

- **上游 Data Bucket SDK：** Commit 数据、提供 bucket / key / session_id、sourceFuncName（FunctionIdentity）、data_addr，调用通知 API，并负责 Session 生命周期。
- **本模块：** TriggerInit、JSON Parser、App Registry、DIRECT Dependency、Event Trigger、Task Builder、Task Router、Delay Queue。
- **下游 Task Coroutine Pool：** 唯一提供 TryEnqueue(TaskInvocation)，负责队列消费、协程调度、SO / Function 加载和执行。

### 2.2 本期基础规则

- 实现语言为 Go；单进程、单个调度实例；Registry 为模块内部唯一实例。
- 对上游只暴露 TriggerInit(appJSONPath) 和 EventTriggerNotify 两个 API。
- TriggerInit 读取 app.json，解析内嵌 functions[]；全部 Function 配置直接内嵌其中。
- DataReadyEvent 仅 Bucket、Key、SessionID。
- sourceFuncName、dataAddr 作为 EventTriggerNotify 独立参数。
- 数据统一通过 dataAddr 传递，不区分大小数据包。
- 不存在 DataRef、INLINE / SHARED_MEMORY、validate / retain / release。
- Key 在 TaskArgument 中必须存在。
- (so_name, func_name) 在当前 App 内唯一。
- DIRECT 按来源的 (so_name, func_name) 查询目标函数。
- FunctionInfo（so_name、func_name、resource）随 TaskInvocation 传给 Pool。
- Pool 只提供 TryEnqueue，不带 ctx；不提供静态 Function 注册 API。
- Task Router 在 TryEnqueue 前执行 CPU / Memory 资源预检查。
- 资源不足、探测失败、计算失败和 Pool NoCapacity 都按延时配置暂存重试或终止；资源类失败不调用 TryEnqueue。
- 不定义 Execution，不实现 Rerun、resource.timeout_ms、跨进程和跨节点调度。

## 3. TriggerInit / App Registry / JSON Parser 需求

### APP-01：TriggerInit 加载 app.json
- **优先级：** P0
- **行为：** TriggerInit 接收 app.json 文件路径，由模块自行读取、解析和校验。
- **验收：** appJSONPath 为空或文件不可读时初始化失败；不开放事件处理。

### APP-02：app.json 内嵌 Function 配置
- **优先级：** P0
- **行为：** 用户按附录 B 编写 app.json；functions[] 每项直接包含 Function 配置，模块不生成业务配置、不读取额外函数配置文件。
- **验收：** Function 必填字段、类型、唯一性和引用均合法后才发布；不支持外部配置路径；数值须无损解析，内存 / 时间换算边界及多 App 延时配置一致性按附录 B 校验。

### APP-03：Bucket 全局唯一
- **优先级：** P0
- **行为：** Bucket 在当前 Registry 的全部 App 范围内全局唯一。
- **验收：** event.Bucket 能唯一定位所属 App。

### APP-04：Function 双标识与资源配置
- **优先级：** P0
- **行为：** functions[] 每项配置 so_name、func_name 和完整 Resource（CPU / Memory / GPU / Disk / Network）。Task Router 必查 CPU / Memory，按需检查 GPU / Disk / Network。
- **验收：** so_name 与 func_name 独立保存；一个 SO 可配置多个函数；不包含未经确认的 execution 配置。

### APP-05：Function 名称唯一性
- **优先级：** P0
- **行为：** (so_name, func_name) 在当前 App 内唯一。
- **验收：** 同一 App 内重复的 (so_name, func_name) 组合会被拒绝；不同共享库可使用同名函数。

### APP-06：DIRECT / Trigger 引用校验
- **优先级：** P0
- **行为：** dependencies 和 triggers 的函数引用必须唯一定位到当前 App 已注册的函数。
- **验收：** 未注册函数引用导致初始化失败。

### APP-07：全局索引
- **优先级：** P0
- **行为：** 建立 Bucket Index、App 内 Function Index、DIRECT Index 和 Trigger Index。
- **验收：** 运行期无需重新解析 JSON。

## 4. DIRECT Dependency 需求

### DIR-01：DIRECT 独立路径
- **优先级：** P0
- **行为：** DIRECT 不作为 Trigger Primitive，单独维护依赖索引。
- **验收：** DIRECT 路径与 Immediate / ByName 路径分离。

### DIR-02：来源 Function 识别
- **优先级：** P0
- **输入：** EventTriggerNotify 的 sourceFuncName。
- **行为：** Function 写出对象时，Object SDK 从 TaskContext 注入 SOName 和 FuncName；外部 Actor 传 FunctionIdentity{}。
- **验收：** 来源的 so_name 和 func_name 均须精确匹配当前 App 的函数定义。

### DIR-03：目标函数展开
- **优先级：** P0
- **行为：** sourceFuncName 对应多个 target func_name 时，每个目标生成独立 Task。
- **验收：** 每个 Task 携带对应 FunctionInfo（so_name + func_name + resource）。

### DIR-04：DIRECT 优先级
- **优先级：** P0
- **行为：** 存在 DIRECT 目标时走 DIRECT，跳过本次 Bucket Trigger；无 DIRECT 目标或来源为零值时进入 Bucket Trigger。
- **验收：** 不同时执行 DIRECT 和 Bucket Trigger。

### DIR-05：DIRECT 输入模式
- **优先级：** P0
- **行为：** 每条 DIRECT 依赖可选 `trigger_option`，整数 `0`（默认，Value）、`1`（Key + Value）、`2`（Key Only）。同一条依赖的所有目标使用同一模式，需要不同模式时拆成多条依赖。同一 App 内重复的 DIRECT 来源／目标对在初始化时拒绝。
- **验收：** TaskArgument 包含必填 Bucket、SessionID、Key 和 DataAddr。

## 5. Event Trigger 需求

### EVT-01：Commit 成功后通知
- **优先级：** P0
- **行为：** 仅数据 Commit 成功后调用 EventTriggerNotify。
- **验收：** 通知携带正确的 Bucket、Key、SessionID、sourceFuncName、dataAddr。

### EVT-02：最小 DataReadyEvent
- **优先级：** P0
- **字段：** Bucket、Key、SessionID。
- **行为：** sourceFuncName 和 dataAddr 分别作为独立参数传入。
- **验收：** DataReadyEvent 不存在 Registry、PayloadType、DataRef、AppName 等字段。

### EVT-03：Immediate
- **优先级：** P0
- **行为：** Bucket 收到有效事件时命中 Immediate。
- **验收：** 目标 func_name 正确。

### EVT-04：ByName
- **优先级：** P0
- **行为：** Bucket 和 Key 完全匹配时命中。
- **验收：** 非匹配 Key 不触发。

### EVT-05：同 Bucket 多 Trigger
- **优先级：** P0
- **行为：** 每条命中 Trigger 独立生成 Task。
- **验收：** 不漏触发、不合并不同目标。

### EVT-06：DIRECT 与 Trigger 路径隔离
- **优先级：** P0
- **行为：** 按 DIR-04 优先级处理。
- **验收：** DIRECT 命中时不再执行 Bucket Trigger。

### EVT-07：事件参数校验
- **优先级：** P0
- **行为：** Bucket、Key、SessionID 必须非空；DataAddr 必须非空。
- **验收：** 非法参数返回 ErrInvalidArgument。

## 6. Task Builder 需求

### TSK-01：输入模式映射
- **优先级：** P0
- **行为：** trigger_option 0 / 1 / 2 分别映射 InputValue / InputKeyValue / InputKeyOnly；DIRECT 按依赖项配置选择，缺省为 InputValue。
- **验收：** InputMode 不再控制数据载体类型。

### TSK-02：TaskArgument 构建
- **优先级：** P0
- **字段：** Bucket、SessionID、Key 和 DataAddr。
- **行为：** 一次通知携带一个对象；每个任务的 Args 长度为 1。
- **验收：** Key 必须；DataAddr 与通知参数一致。

### TSK-03：TaskInvocation 构建
- **优先级：** P0
- **字段：** FunctionInfo、InputMode、Args。
- **行为：** 每个目标 Function 独立构建 TaskInvocation。
- **验收：** TaskInvocation 不携带 DataRef、PayloadType、固定 handler 或 Trigger 内部元数据。

### TSK-04：异步元数据复制
- **优先级：** P0
- **行为：** 复制 Bucket、Key、SessionID、SOName、FuncName 等异步任务需要持有的字符串及 Args 切片；不复制 DataAddr 指向的数据。
- **验收：** EventTriggerNotify 返回后调用方临时字符串失效，不影响排队任务；DataAddr 仍由上游保证有效。

### TSK-05：多目标数据地址共享
- **优先级：** P0
- **行为：** 多个命中目标使用同一 DataAddr，但拥有独立 TaskInvocation / Args 描述符。
- **验收：** 任一任务完成不导致其他任务数据失效。

## 7. Task Router 需求

### RTE-01：Function 资源查询
- **优先级：** P0
- **行为：** 从 TaskInvocation.Function.Resource 读取目标 Function 的 CPU / Memory 需求。
- **验收：** Resource 缺失或非法不得提交任务。

### RTE-02：CPU / Memory 可用资源预检查
- **优先级：** P0
- **行为：** 首次及每次延时重试的 TryEnqueue 前均查询 CPU / Memory 限额和使用量，不得跳过；采样与计算按第四部分 §14.2。
- **实现参考：** /proc/self/cgroup 定位 cgroup；读取 cgroup v1 / v2 CPU、Memory 统计；/proc/meminfo 作为宿主机参考或兜底。
- **验收：** 资源满足才进入 TryEnqueue。

### RTE-03：资源不足分支
- **优先级：** P0（本期仅打印日志）
- **行为：** CPU 或 Memory 不足记录 RESOURCE_INSUFFICIENT；探测 / 计算失败记录 RESOURCE_PROBE_FAILED。均不调用 TryEnqueue，记录原因后判断是否启用延时调度：启用则暂存重试，未启用则终止；后续扩展进程间 / 节点间调度上报。
- **验收：** 资源不足、探测 / 计算失败与 Pool 队列无容量分别记录原因；均覆盖 enabled=true / false 分支。

### RTE-04：原子 TryEnqueue
- **优先级：** P0
- **行为：** 单次 TryEnqueue 完成 Pool 容量判断和入队。
- **验收：** 不拆成 capacity query + enqueue。

### RTE-05：Pool 返回值
- **优先级：** P0
- **返回：** Accepted、NoCapacity、Error。
- **验收：** Accepted 表示 Pool 接收；NoCapacity 表示未接收；Error 表示内部错误。

### RTE-06：成功入队所有权
- **优先级：** P0
- **行为：** Pool 接管 TaskInvocation 元数据副本；不接管、不释放 DataAddr 指向的数据内存。
- **验收：** Router 释放自己的描述符副本后，Pool 仍可正确执行。

### RTE-07：全局 Delay Queue
- **优先级：** P0
- **行为：** 资源不足、探测失败、计算失败或 Pool NoCapacity 时，统一判断 enabled 并使用进程级 Delay Queue；Pool Error 不延时。
- **验收：** enabled、retry_interval_ms、max_wait_ms 语义一致；首次可延时原因固定 deadline；重试和原因切换不重置。重试先查到期再查资源；提交前再次查到期。失败仍按配置延时；队列满拒绝，不产生重复延时项。

### RTE-08：延时到期
- **优先级：** P0
- **行为：** 达到 max_wait_ms 后记录 TASK_DELAY_EXPIRED 并终止任务。
- **验收：** 释放 Router 自有元数据，不释放 DataAddr。

### RTE-09：不调用 wake_one
- **优先级：** P0
- **行为：** 本模块不主动唤醒协程。
- **验收：** Pool 内部保证成功入队任务最终可被消费者发现。

## 8. 上下游协作需求

### API-01：TriggerInit
- **优先级：** P0
- **行为：** 本模块提供 TriggerInit(appJSONPath)，自行读取 app.json（包含内嵌 Function 配置）。
- **验收：** 不要求上游调用 Registry 创建、注册、封存和销毁 API；全部初始化成功结束前不得发通知；不得并发初始化与通知。

### API-02：EventTriggerNotify
- **优先级：** P0
- **行为：** 本模块提供 EventTriggerNotify(event, sourceFuncName, dataAddr)。
- **验收：** DataReadyEvent 只有 Bucket、Key、SessionID；数据地址和来源函数为独立参数。

### API-03：Pool TryEnqueue
- **优先级：** P0
- **行为：** 下游只提供 TryEnqueue(task TaskInvocation)；实例创建和装配由下游 / 集成方负责，本模块仅调用 API。
- **验收：** 不带 ctx，不存在 register_function / unregister_function / wake_one Adapter 回调。

### API-04：FunctionInfo 透传
- **优先级：** P0
- **行为：** TaskInvocation 携带 SOName、FuncName 和 Resource。
- **验收：** 不需要 Pool 静态 Function 注册 API。

### API-05：数据地址责任边界
- **优先级：** P0
- **行为：** Data SDK 管控数据有效性、任务完成判定及回收。本模块不实现或要求新增 Session 完成判定契约。
- **验收：** 本模块不释放或复用借用地址；联调使用 SDK 提供的有效数据，不验收 SDK 内部完成判定机制。

## 9. 可观测性与安全

### OBS-01：结构化日志
- **行为：** 日志可包含 trace_seq、app_name、bucket、key、session_id、source_so_name、source_func_name、so_name、func_name、route_path、trigger_name、resource_check、cpu_available、memory_available、pool_result、delay_state 和 elapsed_ms。
- **约束：** 内部诊断字段不得因此进入公共 API / TaskInvocation。

### SAFE-01：SO 名称校验
- **行为：** so_name 只能是安全文件名，不允许路径穿越或任意路径加载。

### SAFE-02：Function 标识校验
- **行为：** 函数引用必须唯一定位到当前 App 已注册的 Function；本模块校验配置引用，不负责实际 SO 符号加载。

### SAFE-03：数据地址借用
- **行为：** Scheduler / Pool 不得释放 DataAddr；上游按 Session 生命周期保证地址有效。

## 10. 本期明确不实现

- DataRef、validate、retain、release 和引用计数。
- INLINE / SHARED_MEMORY 双载体和大小阈值选择。
- Scheduler 主动调用 Data SDK 查询数据。
- Pool 静态 Function 注册和未经确认的 Execution。
- Rerun、Function 执行超时、resource.timeout_ms。
- wake_one。
- 未列入核心模块详细设计的触发原语。
- Node / Process Scheduler、Warm / Cold Executor、跨进程和跨节点分发。
- Task Coroutine Pool 内部协程、队列和 SO Loader 实现。
- 资源结果的外部上报（后续扩展进程间 / 节点间调度）。

## 11. 需求完成判定

1. TriggerInit 能读取 app.json（包含内嵌 Function 配置） 并建立唯一 Registry。
2. Function 通过 so_name + func_name 独立标识。
3. DIRECT 按来源的 (so_name, func_name) 匹配。
4. Immediate / ByName 匹配正确。
5. EventTriggerNotify 只接收最小事件结构和独立数据参数。
6. Task Builder 构建正确的 Args[]。
7. Task Router 先检查 CPU / Memory，再调用 TryEnqueue。
8. Pool 唯一接口为不带 ctx 的 TryEnqueue。
9. 数据有效性、任务完成判定及回收由 Data SDK 管控，本模块不释放借用地址。
10. 不存在 DataRef、双数据载体、Pool 静态 Function 注册和 Execution 配置。

验收证据按[第五部分](#part-5)和[附录 E](#part-11)分阶段提供：P0 需求先通过单元及本模块链路，再用 Mock SDK / Fake Pool 验证最终输出、异常和并发；真实上下游联调独立记录。Mock 测试不新增业务 API、Session 回收机制或真实 Pool 装配职责。文档中的需求完成判定不是测试已通过声明。


## 12. 资源准入补充需求

| ID | 功能 | 优先级 |
|---|---|---|
| RTE-10 | GPU 准入 | P0 |
| RTE-11 | 磁盘准入 | P0 |
| RTE-12 | 网络准入 | P0 |
| RTE-13 | 多资源联合准入与延时 | P0 |

---

<a id="part-7"></a>

## 附录 A：Pheromone 源码映射与 V0.6 裁剪说明

## A.1 参考范围

参考仓库：[MincYu/pheromone](https://github.com/MincYu/pheromone)。

本项目借鉴 Pheromone 的数据驱动执行模型和 Trigger / DIRECT 语义，不复制其完整分布式系统。本轮固定参考提交为 `4d22677aa20dd94069da06d47ec2608504bf6bf4`；已复核 DIRECT 优先分支、Immediate / ByName、输入模式和执行消息相关源码。源码链接固定到该提交；V0.6 的地址传递和 Go API 是本项目裁剪契约，不等同于上游协议。

## A.2 两级调度取舍

| Pheromone | 原职责 | V0.6 |
|---|---|---|
| Coordinator | 选择 Worker Node、维护节点状态和数据位置 | 不实现 |
| Local Scheduler | 选择本地 Executor、处理本地任务调度 | 只保留本地 Task Router 最小提交职责 |
| Warm / Cold Executor | 优先复用已加载 Function 的 Executor | 不实现；下游 Pool 管理执行器 |
| Delay Call Queue | 本地暂存等待资源的 Function Call | 保留为本地 Delay Queue，采用全局配置 |
| Remote Forward | 本地资源不足时转发其他节点 | 不实现，只留日志和扩展点 |

参考源码：
- [func_call_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/func_call_handler.cpp)
- [notify_handler.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/notify_handler.cpp)
- [scheduler_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp)

V0.6 不声称实现 Pheromone 完整两级调度。当前仅覆盖单节点、单进程内的任务准入和延时重试。

## A.3 DIRECT Dependency

Pheromone 的 AppRegistration / DataDependency 使用 DIRECT 类型以及来源函数、目标函数集合。Local Scheduler 在处理 Function 输出对象时先查来源函数是否存在 DIRECT 依赖；存在则调度目标函数，不再进入普通 Bucket Trigger 检查。

参考：
- [operation.proto：DataDependency / AppRegistration](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/proto/operation.proto)
- [app_register_handler.cpp：DIRECT 依赖保留](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/app_register_handler.cpp)
- [scheduler_server.cpp：DIRECT 分支优先于 Trigger 检查](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp)

V0.6 保留 DIRECT 独立路径、来源 Function 到多个目标 Function 的映射，以及 DIRECT 优先于 Bucket Trigger 的规则。每条 DIRECT 配置只声明一个来源函数，多个目标放入 tgt_functions；DIRECT 的输入模式可由依赖项配置，缺省为 InputValue。固定提交的上游 `DataDependency` 没有 `trigger_option`，本地 DIRECT 分支依据数据打包方式构造参数和 `arg_flag`。0 / 1 / 2 三种输入语义取自上游 Bucket Trigger；在 DIRECT 配置中开放这三项是 V0.6 的设计。

## A.4 Bucket 与 Trigger

Pheromone 的 Trigger 输入核心是 BucketKey（Bucket、Key、Session）；Trigger 根据自身配置产生 TriggerAction。Bucket 到 Trigger 的索引是一个列表，一个 Bucket 可以关联多个 Trigger。

参考：
- [trigger.hpp：BucketKey、TriggerAction、ImmediateTrigger、ByNameTrigger](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/common/include/trigger.hpp)
- [notify_handler.cpp：遍历 Bucket Trigger 列表](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/coordinator/notify_handler.cpp)

V0.6 保留 Immediate、ByName、同 Bucket 多 Trigger 独立匹配和 Trigger Option 0 / 1 / 2。

## A.5 Data Bucket 与通知字段

Pheromone 的 KVS 通知包含 BucketKeyAddress、Payload、数据地址和通信控制信息。V0.6 裁剪为：
- DataReadyEvent：bucket、key、session_id；
- sourceFuncName：独立参数，包含 so_name 和 func_name；
- data_addr：独立参数，统一地址传递。

V0.6 不接收 app_name、active_trigger_names、ips、response_address、request_id、trigger_name、target_function、payload_type 或 DataRef。Bucket 在当前 Registry 全局唯一，本模块自行反查 App。

## A.6 下游执行消息

Pheromone Local Scheduler 向 Executor 发送的本地执行消息核心是函数标识、Session、参数模式和参数内容；Trigger 元数据不传给 Executor。

V0.6 收敛为 TaskInvocation：
- SessionID；
- FunctionInfo：so_name、func_name、resource；
- InputMode；
- Args[]：Key 和 DataAddr。

FunctionInfo 随每个 TaskInvocation 通过 TryEnqueue 传递，不通过单独静态注册 API 交付。Pool 自行消费队列和唤醒协程。

参考：[scheduler_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/scheduler/scheduler_server.cpp)、[executor_server.cpp](https://github.com/MincYu/pheromone/blob/4d22677aa20dd94069da06d47ec2608504bf6bf4/src/executor/executor_server.cpp)。

## A.7 本期明确裁剪

- 不实现 Coordinator、Node Scheduler、Process Scheduler。
- 不实现 Warm / Cold Executor 选择。
- 不实现 Rerun、Function 执行超时和独立 wake_one。
- 不实现跨进程、跨节点任务转发。
- 不实现 Data Bucket 存储、共享内存分配、1KB 阈值和底层消息传输。
- 不实现 DataRef、validate、retain、release 和双载体选择。
- 不实现 Pool 静态 Function 注册、Execution 配置或本模块内 SO Loader。
- 不实现 Task Coroutine Pool 内部队列、协程和 Function 执行。

---

<a id="part-8"></a>

## 附录 B：app.json 配置协议与示例

## B.1 配置原则

V0.6 仅使用 app.json。用户根据本文字段说明和示例自行完成编写，再将文件路径传入 TriggerInit；模块自行读取、解析和校验，不负责生成或自动补写业务配置。

app.json 包含 App 元信息、内嵌 functions[]、Bucket、DIRECT、Trigger 和 Delay Queue。每项 Function 包含 so_name、func_name、description、version、resource；CPU / Memory 是本期必做准入维度，GPU / Disk / Network 按任务声明检查并透传。全部初始化成功结束后才允许发送 EventTriggerNotify。

## B.2 app.json 完整示例

~~~json
{
  "app_name": "smc_demo",
  "description": "单节点单进程数据驱动示例",
  "version": "0.6.0",
  "scheduler": {
    "delay_queue": {
      "enabled": true,
      "retry_interval_ms": 10,
      "max_wait_ms": 1000
    }
  },
  "functions": [
    {
      "so_name": "producer.so",
      "func_name": "producer",
      "description": "处理输入并产生普通 Bucket 数据",
      "version": "1.0.0",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 128,
        "gpu": {
          "required": false,
          "device_count": 0,
          "memory_mb": 0
        },
        "disk_mb": 0,
        "network_mbps": 0
      }
    },
    {
      "so_name": "processor.so",
      "func_name": "processor",
      "description": "消费 Bucket Trigger 输入",
      "version": "1.0.0",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 256,
        "gpu": {
          "required": false,
          "device_count": 0,
          "memory_mb": 0
        },
        "disk_mb": 0,
        "network_mbps": 0
      }
    },
    {
      "so_name": "direct_source.so",
      "func_name": "direct_source",
      "description": "DIRECT 依赖的来源函数",
      "version": "1.0.0",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 128,
        "gpu": {
          "required": false,
          "device_count": 0,
          "memory_mb": 0
        },
        "disk_mb": 0,
        "network_mbps": 0
      }
    },
    {
      "so_name": "direct_processor.so",
      "func_name": "direct_processor",
      "description": "DIRECT 依赖的目标函数",
      "version": "1.0.0",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 128,
        "gpu": {
          "required": false,
          "device_count": 0,
          "memory_mb": 0
        },
        "disk_mb": 0,
        "network_mbps": 0
      }
    }
  ],
  "buckets": [
    "b_input",
    "b_shared",
    "b_direct_output",
    "b_result"
  ],
  "dependencies": [
    {
      "type": "DIRECT",
      "src_functions": [
        "direct_source"
      ],
      "tgt_functions": [
        "direct_processor"
      ],
      "trigger_option": 0
    }
  ],
  "triggers": [
    {
      "bucket": "b_input",
      "trigger_name": "t_input_immediate",
      "primitive_type": "IMMEDIATE",
      "primitive": {
        "function": "producer"
      },
      "trigger_option": 0
    },
    {
      "bucket": "b_shared",
      "trigger_name": "t_shared_immediate",
      "primitive_type": "IMMEDIATE",
      "primitive": {
        "function": "processor"
      },
      "trigger_option": 0
    },
    {
      "bucket": "b_shared",
      "trigger_name": "t_shared_byname_ready",
      "primitive_type": "BY_NAME",
      "primitive": {
        "function": "processor",
        "key_name": "ready"
      },
      "trigger_option": 1
    }
  ]
}
~~~

## B.3 用户编写与启动步骤

1. 确定 App 名称和 Bucket 名称；Bucket 必须跨 App 唯一。
2. 在 functions[] 逐项填写 SO 名、函数名和资源需求；共享库名与函数名的组合在当前 App 内唯一。
3. 用这些函数名编写 DIRECT 和 Trigger；设置 Delay Queue。
4. 保存为 app.json，调用 TriggerInit(appJSONPath) 并检查返回 error；失败则修改配置后重试。
5. 若有多个 App，依次完成全部初始化；成功结束后才允许发送事件，初始化与事件不得并发。

## B.4 字段定义

### app.json 顶层

| 字段 | 类型 | 约束 |
|---|---|---|
| app_name | string | 必填；Registry 内唯一 |
| description | string | 可选 |
| version | string | 必填 |
| scheduler.delay_queue.enabled | boolean | 必填；资源不足、探测失败、计算失败及 Pool NoCapacity 共用此开关 |
| scheduler.delay_queue.retry_interval_ms | integer | 必填；1..9223372036854；上述可延时原因的重试间隔 |
| scheduler.delay_queue.max_wait_ms | integer | 必填；1..9223372036854，且不小于 retry_interval_ms；从首次可延时原因计时，重试不重置 |
| functions | array(object) | 必填，至少一个 |
| functions[] | object | 直接包含下表 Function 字段；不接受外部配置路径 |
| buckets | array(string) | 必填；跨 App 全局唯一 |
| dependencies | array(object) | 可为空；DIRECT |
| triggers | array(object) | 可为空；IMMEDIATE / BY_NAME |

### functions[] 每项字段（下列路径均相对该项）

| 字段 | 类型 | 约束 |
|---|---|---|
| so_name | string | 必填；安全 SO 文件名，不允许路径分隔符 |
| func_name | string | 必填；与 so_name 组成 App 内唯一标识 |
| description | string | 可选 |
| version | string | 可选 |
| resource.cpu_cores | number | 必填；有限且大于 0 |
| resource.memory_mb | integer | 必填；1..17592186044415，换算字节后可由 uint64 表示 |
| resource.gpu.required | boolean | 可选；默认 false |
| resource.gpu.device_count | integer | 可选；默认 0；非负 |
| resource.gpu.memory_mb | integer | 可选；默认 0；非负 |
| resource.disk_mb | integer | 可选；默认 0；非负 |
| resource.network_mbps | integer | 可选；默认 0；非负 |

Function 配置不包含 execution、timeout_ms；GPU、Disk、Network 字段为可选资源需求，由 Task Router 按工程实现设计 §14.7 独立进行准入判断。

### DIRECT Dependency

| 字段 | 类型 | 约束 |
|---|---|---|
| type | string | DIRECT |
| src_functions | array(函数引用) | 必填；恰好一个来源函数 |
| tgt_functions | array(函数引用) | 必填；至少一个目标函数 |
| trigger_option | integer | 可选；在 `dependencies[]` 中仅 DIRECT 支持；0 = Value（默认），1 = Key + Value，2 = Key Only |

来源和目标必须属于同一 App 的 functions。函数引用可写为 `{"so_name":"reader.so","func_name":"read"}`。字符串简写 `"read"` 仅在当前 App 内恰好有一个同名函数时有效；同名函数存在于多个共享库时，必须用对象指定库名和函数名。一个来源可对应多个目标；多个来源拆成多条依赖配置。

字段写在 `dependencies[]` 的 DIRECT 对象内。完整 `app.json` 示例显式设置 `0`，目标函数收到 Value。省略字段时效果相同。以下两段配置可分别替换完整示例中的同一条依赖。

**Key + Value（1）**

~~~json
{
  "type": "DIRECT",
  "src_functions": [
    "direct_source"
  ],
  "tgt_functions": [
    "direct_processor"
  ],
  "trigger_option": 1
}
~~~

**Key Only（2）**

~~~json
{
  "type": "DIRECT",
  "src_functions": [
    "direct_source"
  ],
  "tgt_functions": [
    "direct_processor"
  ],
  "trigger_option": 2
}
~~~

选择 `2` 时，Pool 向目标函数提供 Key。所有目标共用当前依赖项的模式。

### Bucket 与 Trigger

| 字段 | 类型 | 约束 |
|---|---|---|
| buckets[] | string | 当前 Registry 全局唯一 |
| bucket | string | Trigger 必填，必须在本 App buckets 中 |
| trigger_name | string | App 内唯一 |
| primitive_type | string | IMMEDIATE / BY_NAME |
| primitive.function | 函数引用 | 已注册的目标函数，支持 (so_name, func_name) 对象或无歧义的字符串简写 |
| primitive.key_name | string | BY_NAME 必填；IMMEDIATE 禁止 |
| trigger_option | integer | 0 / 1 / 2；默认 0 |

`trigger_option` 是 `app.json` 中 DIRECT 依赖项和 Bucket Trigger 的可选整数配置，省略时按 `0` 处理。解析配置时，`0`、`1`、`2` 分别映射为 Go 类型 `InputMode` 的 `InputValue`、`InputKeyValue`、`InputKeyOnly`，运行时写入 `TaskInvocation.InputMode`。 同一条 DIRECT 依赖的所有目标使用同一模式。不同目标需要不同模式时，拆成多条依赖。初始化时拒绝同一 App 内重复的 DIRECT 来源／目标对。

### 资源探测配置

| 路径 | 约束 |
|---|---|
| scheduler.resource_probe.disk_path | 有磁盘需求时必填；已存在的绝对路径 |
| scheduler.resource_probe.network_interface | 有网络需求时必填；目标网卡名 |
| scheduler.resource_probe.network_limit_mbps | 有网络需求时必填；部署方提供的正整数带宽上限，单位 Mbps |

各维度准入算法见工程实现设计 §14.7。

## B.5 校验规则

1. 拒绝非法 JSON、重复字段、未知字段、错误类型和超长输入。
2. app_name 在 Registry 内唯一；Bucket 跨全部 App 全局唯一。
3. functions[] 必须是非空对象数组，Function 字段直接内嵌。
4. 每项 Function 结构必须合法，so_name、func_name 必填；拒绝外部配置路径。
5. (so_name, func_name) 组合在当前 App 内唯一；同一个 so_name 可包含多个函数，不同 so_name 可声明同名函数。
6. 同一 so_name + func_name 被多个 App 复用时，Resource 必须一致。
7. 依赖只接受 DIRECT，其他类型在初始化时拒绝。每条 DIRECT 配置必须只有一个 src_functions 项，tgt_functions 至少一个；来源和目标都必须存在。
8. Trigger 的 Bucket 必须存在，目标函数引用必须能唯一解析；trigger_name 在 App 内唯一。
9. 仅接受 IMMEDIATE、BY_NAME；ByName 必须配置非空 key_name，Immediate 禁止 key_name。
10. trigger_option 只允许 0、1、2。
11. Delay Queue enabled 必须为布尔值；时间间隔和最大等待时间必须为正整数。
12. 拒绝 execution、resource.timeout_ms、DataRef 和旧的双数据载体字段；Resource 中 GPU、Disk、Network 数值必须非负。
13. 当前 App 的全部配置必须整体校验成功后才发布；任一内嵌 Function 校验失败不得留下部分 AppRecord。
14. 所有整数字段无损解析，不经 float64 中转：gpu.device_count 在 uint32 范围，其余无符号资源整数字段在 uint64 范围；拒绝小数、负数或超范围值。CPU 必须为可表示的有限正浮点数。
15. retry_interval_ms / max_wait_ms 换算 time.Duration 前检查 int64 纳秒上限；memory_mb 换算字节前检查 uint64 上限。超界在 TriggerInit 阶段拒绝，不留到运行时反复延时；运行时样本计算仍做防御检查。
16. 多 App 的 delay_queue 三项有效配置必须完全一致；不一致拒绝当前 App，不改动已发布 Registry 和进程级延时配置。


## B.6 资源配置示例

可将以下对象并入现有 app.json：

~~~json
{
  "scheduler": {
    "resource_probe":{"disk_path":"/var/lib/pheromone","network_interface":"eth0","network_limit_mbps":1000}
  }
}
~~~

`scheduler.delay_queue` 仍为必填。

---

<a id="part-9"></a>

## 附录 C：Go 接口与 Task 契约

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
    Bucket    string
    SessionID string
    Key       string
    DataAddr  unsafe.Pointer
}

type TaskInvocation struct {
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
    sourceFuncName FunctionIdentity,
    dataAddr unsafe.Pointer,
) error
~~~

用户根据附录 B 自行编写 app.json。全部初始化成功结束后才允许发送通知，禁止初始化与通知并发；初始化未完成返回 ErrNotInitialized。完整状态规则见第三部分 §11.6。

TriggerInit 只接收 app.json 路径。模块自行读取 app.json，并解析内嵌 functions[]。

EventTriggerNotify 的 DataReadyEvent 只包含 Bucket、Key、SessionID。sourceFuncName、dataAddr 是独立参数。外部 SMC Actor 传 FunctionIdentity{}。

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
    SOName    string
    FuncName  string
    InputMode InputMode
    Args      []TaskArgument
}
~~~

TaskContext 是 Pool 与 Function / Object SDK 的逻辑执行上下文，不是本模块的第三个上游 API。Pool 应确保当前任务的 SOName 和 FuncName 能被 Object SDK 读取。

Function 通过 Object API 写入输出数据。Object SDK 从 TaskContext 的 SOName 和 FuncName 构造 EventTriggerNotify 的 sourceFuncName。外部 SMC Actor 传 FunctionIdentity{}。

本模块不规定 Function 是 Go Plugin、cgo/native SO 还是其他执行载体；实际加载与调用 ABI 由下游 Pool 负责。

## C.6 错误语义

两个上游 API 成功返回 `nil`，失败统一返回 `*APIError`，字段为 `StatusCode`（`uint16`）和 `Msg`（非空诊断文本）。调用方以 `errors.As` 提取错误并按状态码分支。类型定义与各接口适用状态见[第三部分 §10.5.6、§10.6](#part-3)。下游 `TryEnqueue` 仍只返回 `EnqueueResult`；任务路由结果不直接转换为上游错误。

## C.7 所有权与生命周期

| 对象 | 创建方 | 使用方 | 所有权 / 有效期 |
|---|---|---|---|
| DataReadyEvent | 上游 SDK | EventTriggerNotify | 上游保证调用期间有效 |
| sourceFuncName | 上游 SDK / TaskContext | DIRECT 匹配 | 上游保证调用期间有效 |
| Bucket / Key / SessionID 字符串 | 上游 SDK | Task Builder / Pool / Function | Task Builder 复制异步任务所需字符串 |
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

---

<a id="part-10"></a>

## 附录 D：数据结构、所有权与错误码

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
      bucket
      session_id
      key
      data_addr
    }
    class TaskInvocation {
      FunctionInfo
      InputMode
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

EventTriggerNotify 独立接收 sourceFuncName、dataAddr。外部 Actor 传 FunctionIdentity{}。

不传 AppName、EventID、Trigger 配置、目标函数、PayloadType、DataRef、IP 或 RPC 地址。

## D.3 TaskInvocation 最小字段

| 字段 | 必须 | 说明 |
|---|---|---|
| SessionID | 是 | 业务 Session |
| Function.SOName | 是 | 下游识别的 SO 文件名 |
| Function.FuncName | 是 | 目标函数标识 |
| Function.Resource | 是 | 完整 CPU / Memory / GPU / Disk / Network 静态资源需求；Router 必查 CPU / Memory，按需检查 GPU / Disk / Network |
| InputMode | 是 | InputValue / InputKeyValue / InputKeyOnly |
| Args | 是 | 参数数组；长度为 1 |

每个 TaskArgument 包含必填 Bucket、SessionID、Key 和 DataAddr。DataAddr 必须非空。

## D.4 输入模式

| 路径 | 输入模式 | Function 输入语义 |
|---|---|---|
| DIRECT | 由依赖项 trigger_option 选择；缺省 InputValue | Value / Key + Value / Key Only |
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
7. Data SDK 管控 Session 完成判定和回收，Scheduler 不承担该机制。
8. 一个 Task 完成不能导致同一 Session 其他 Task 正在使用的数据被回收。

## D.6 DIRECT 与 Trigger 路径

- sourceFuncName 有 DIRECT targets：构建 DIRECT Task，跳过 Bucket Trigger。
- sourceFuncName 为零值或无 DIRECT targets：执行 Bucket Trigger 匹配。
- 一个 DIRECT source 对应多个 target 时，每个 target 生成独立 Task。
- 一个 Bucket 多条 Trigger 命中时，每条规则生成独立 Task，不去重。
- DIRECT 读取依赖项的 trigger_option，Bucket Trigger 读取各自的 trigger_option。二者均缺省为 InputValue。

## D.7 Pool 返回值与 Router 行为

| Pool 返回 | Router 行为 | 是否入队 |
|---|---|---:|
| Accepted | 删除 Router PendingTask；Pool 接管任务描述符副本 | 是 |
| NoCapacity + Delay enabled | 放入 Delay Queue | 否 |
| NoCapacity + Delay disabled | 记录 PROCESS_DISPATCH_REQUIRED，释放 Router 元数据 | 否 |
| Error | 终止并记录内部错误 | 否 |
| Delay expired | 记录 TASK_DELAY_EXPIRED，释放 Router 元数据 | 否 |

CPU / Memory 资源不足、探测失败和计算失败发生在 TryEnqueue 之前，不调用 Pool。三者均记录原因后判断 scheduler.delay_queue.enabled：启用则暂存重试，否则终止本次路由。与 Pool NoCapacity 共用同一 Delay Queue；Pool Error 不延时。

## D.8 错误语义

两个上游 API 成功返回 `nil`，失败统一返回 `*APIError`，字段为 `StatusCode`（`uint16`）和 `Msg`（非空诊断文本）。调用方以 `errors.As` 提取错误并按状态码分支。类型定义与各接口适用状态见[第三部分 §10.5.6、§10.6](#part-3)。下游 `TryEnqueue` 仍只返回 `EnqueueResult`；任务路由结果不直接转换为上游错误。

## D.9 Delay Queue 配置和状态

Delay Queue 为进程级全局配置。多个 App 声明的配置必须完全相同。具体字段：

- enabled：是否启用。
- retry_interval_ms：资源复查及可能的提交尝试之间的重试间隔；下一次唤醒不晚于固定 deadline。
- max_wait_ms：从首次资源不足、探测失败、计算失败或 Pool NoCapacity 到终止的最大等待时长；后续重试及原因变化不重置 deadline。

每次重试先查到期，再执行资源检查；调用 TryEnqueue 前再次查到期。持续失败按 retry_interval_ms 重试直到 deadline，日志区分具体原因。

时间使用单调时钟。队列达到实现级容量上限时，拒绝新项并释放 Router 持有的元数据。Delay Queue 只重试提交，不重新执行已经进入 Pool 的 Function。

---

<a id="part-11"></a>

## 附录 E：Mock 端到端测试与独立验收


## E.1 目的与验收边界

上下游模块并行开发期间，通过可控测试替身运行真实调度模块，独立验证从 app.json 初始化、事件通知、规则匹配、Task 构建、资源检查、延时重试到测试函数最终输出的完整链路。

本附录是待实现的测试设计，不是测试执行报告。用例列入文档、示例可解析或文档一致性检查通过，不代表 Go 实现及功能测试通过。

| 阶段 | 真实执行范围 | 替身 / 外部依赖 | 可给出的结论 |
|---|---|---|---|
| A：本模块独立验收 | 配置、Registry、DIRECT、Trigger、Task Builder、Router、Delay Queue；真实 Linux 资源读取适配器 | 事件发生器、Fake Pool、可控资源样本和时钟 | 本模块独立验收通过，具备联调条件 |
| B：Mock 端到端验收 | A 的完整调度链路，增加异步消费、测试函数执行和输出事件回流 | Mock SDK、Fake Pool、测试函数；可控资源与时钟 | Mock 端到端验收通过 |
| C：真实上下游联调 | 真实 SDK Commit、调度模块、真实 Pool 和实际函数调用 | 真实上下游团队提供环境和实现 | 指定环境、接口版本下集成验收通过 |

A、B 不等待真实 SDK 或 Pool 完工；C 单独记录为待联调，不阻塞 A、B 结论。A、B 的通过不能证明真实 Pool 的原子入队、唤醒、SO 加载能力或 SDK 的内部回收机制。

## E.2 测试环境与依赖替换

| 组件 | 采用方式 | 测试职责 |
|---|---|---|
| app.json / TriggerInit | 真实文件、真实 API | 文件读取、解析、字段和引用校验、初始化状态 |
| Registry / DIRECT / Trigger | 真实实现 | App 隔离、优先级、多目标与输入模式 |
| Task Builder / Router / Delay Queue | 真实实现 | 描述符组装、资源判断、Pool 返回分支、重试和超时 |
| Mock SDK / 事件发生器 | 测试代码 | 分配并保持数据，模拟 Commit 完成后调用真实通知 API；接收测试函数输出并通知下一步 |
| Fake Pool | 实现同一 TaskPool 契约 | 按脚本返回 Accepted / NoCapacity / Error；复制接收描述符、异步消费和分发测试函数 |
| 资源探测替身 | 注入样本或故障 | 按脚本提供充足、不足、读取失败、解析失败及非法计算样本 |
| 文件系统替身 | procfs / cgroup 文本夹具 | 用真实解析和计算实现验证 v1 / v2、无限额、祖先限额和文件错误，不只 Mock 最终布尔结论 |
| 测试时钟 | 单调时间、定时器统一替换 | 推进采样、重试与 deadline；精确验证边界 |
| 测试函数 | 测试包中的 Go 函数 | 检查 TaskContext 逻辑输入、转换数据、生成最终结果；不验证 SO Loader |
| 日志采集器 | 测试收集结构化日志 | 核对原因、延时状态及超时记录 |

仅替换外部依赖，禁止在 Mock 中重写 DIRECT、Trigger、任务组装或重试决策。预期目标、字段、次数和输出根据夹具预先独立给出，不用被测模块自身计算结果生成预期。

依赖替换通过 internal 包和测试构建的组合入口完成；对上游仍仅 TriggerInit、EventTriggerNotify，对下游仍仅 TryEnqueue。不新增 SetPool、Reset、Tick、Seal 等客户 API；测试环境装配由测试夹具完成，不扩大本模块对真实 Pool 实例装配的职责。

Mock 与被测模块引用同一份生产类型及接口定义，编译时检查 Fake Pool 满足 TaskPool，禁止各维护一套简化版 TaskInvocation。固定设计 / 接口版本；后续真实 Pool 接入时复用相同契约检查，防止 Mock 和生产接口各自演进。

单实例 Registry 的不同 app.json 用例默认用独立测试进程，避免跨用例状态污染。内部单元测试可以创建独立私有运行对象；模拟客户调用的链路测试必须使用真实两个上游 API，不能绕过解析或初始化。初始化串行结束后才并发发送事件。

## E.3 Mock SDK 与数据内存

1. 基础链路夹具使用 Linux mmap 分配的稳定内存，保存输入数据与长度，完成写入后才通知。不得把未固定 Go Heap 指针作为异步借用数据地址。
2. 测试函数通过测试用 Object SDK 辅助层写出新对象；写入完成后调用真实 EventTriggerNotify。来源标识从当前执行任务的 SOName 和 FuncName 注入，外部 Actor 传 FunctionIdentity{}。
3. 同一 Session / Bucket / Key 的测试对象写入后保持不可变；使用不同 Key 表示新对象，不通过覆盖旧对象制造生命周期歧义。
4. 夹具持有全部测试映射，直到事件发生器停止、在途通知退出、内部延时项结束且 Fake Pool 消费退出，才统一解除映射；断言前后字节内容和地址一致性。
5. 此处等待和清理仅用于测试夹具管理内存，不要求调度模块实现 Session 完成判定或生产回收协议。SDK 内部回收由 SDK 团队负责。
6. Value / Key+Value / Key Only 分别检查逻辑输入语义。TaskArgument 仍携带必填 Key 和原始地址；Key Only 测试函数不得解引用 Value。

## E.4 Fake Pool 契约与自检

Fake Pool 使用带锁的有界队列，容量判断和入队在同一临界区内完成。用例可以按目标函数及第几次提交配置返回序列，避免并发线程先后顺序影响脚本归属。

- Accepted：返回前复制 TaskInvocation 的 Args 切片及需要独立持有的元数据；只借用 DataAddr，不复制、不释放业务数据。为 Accepted 任务分配测试内部记录号，记录接收与消费次数。
- NoCapacity / Error：不入队、不执行、不保存调用方可变 Args 切片；测试日志可保存独立字段副本作为诊断证据。
- TryEnqueue 只做接收，不在调用栈内同步执行测试函数。独立 worker 通过队列和同步屏障异步执行；用例可以延迟消费以验证异步边界。
- 测试分发表以 SOName + FuncName 定位 Go 测试函数；这只是 Fake Pool 机制，不是本模块静态 Function 注册 API，不约定真实 SO 加载方式。
- 测试函数输出经 Mock SDK 回到真实 EventTriggerNotify；不得由 Fake Pool 直接调用 Trigger 或绕过通知。
- 测试开始前自检 Accepted 的复制边界、拒绝不消费、队列容量并发约束及异步执行。自检失败则整套 Mock 结果无效。

Fake Pool 的容量和复制自检只证明替身可用，不能用来验收真实 Pool 的内部实现。我们验收的是资源不满足时不调用 Pool、正确解释返回值、Accepted 后不重复提交以及任务字段和生命周期边界符合契约。

## E.5 时钟、资源与并发控制

同一个可推进的单调时钟覆盖 Resource Probe 采样、快照有效期、Router 重试和 deadline。推进时钟后，等待调度器完成本轮处理的明确同步信号，再检查结果；不能用固定 sleep 猜测完成时间。真实墙钟超时仅作为测试卡死的退出保护。

资源脚本既要覆盖 Router 所需快照，也要使用原始文件样本验证实际解析和 CPU / Memory 计算；资源不足、探测失败和计算失败不得混为同一种输入。首次可延时原因建立 FirstDeferred / deadline，后续原因切换和重试不重置。资源检查过程中跨过 deadline 的场景必须覆盖，提交前再次确认未超时。

并发测试使用开始屏障、提交 / 消费回执、完成计数和带截止时间的等待。只有模块约定顺序时才断言全局顺序；其余比较任务集合、每次事件预期扇出数量、字段与每项接收 / 消费次数。测试内部记录号不得加入生产 API 或 TaskInvocation。

不同目标的资源脚本独立绑定，不能因为探测缓存而让一个目标的故障串到另一个目标；共享快照的生产测试另行验证其合法缓存语义。

## E.6 Mock 端到端场景矩阵

本表是第 05 文档现有 88 项编号检查的链路补充，每行可展开多个参数化用例；不是新增业务功能，也不是已经执行的测试数量。

| 用例 | 场景 / 故障注入 | 必须断言 | 需求 / 既有检查 |
|---|---|---|---|
| MOCK-E2E-01 | 合法 app.json → Immediate → 异步测试函数 → 最终结果 | 配置真实读取，目标、输入模式、Args、输出字节正确，成功接收和执行各一次 | APP、EVT、TSK、API |
| MOCK-E2E-02 | ByName 精确命中及非命中 | 匹配 Key 才生成任务，非命中无 Pool 调用 | EVT-04 |
| MOCK-E2E-03 | A 写出对象，SDK 注入来源，DIRECT 触发 B；同 Bucket 另有 Trigger | B 由 DIRECT 触发，该次事件不额外走 Bucket Trigger；最终输出正确 | DIR-02–05、EVT-06 |
| MOCK-E2E-04 | 同事件多 Trigger / DIRECT 多目标，分别测试 | 目标集合和独立描述符正确，共享原地址；每个合法命中分别接收 | DIR-03、EVT-05、TSK-05 |
| MOCK-E2E-05 | Value、Key+Value、Key Only 参数化流水线 | 输入模式和 Key 符合配置；Key Only 不读 Value；Args 长度为 1 | TSK-01–03 |
| MOCK-E2E-06 | CPU / Memory 不足，分别 enabled=true / false | 首次不调用 Pool；开启则等待，恢复后执行，关闭则日志并终止 | TEST-RTE-02–03、22 |
| MOCK-E2E-07 | 资源文件读取 / 解析失败，分别 enabled=true / false | 不使用异常值放行，日志 reason 正确；开启恢复后执行，关闭终止 | TEST-RTE-06、12、23 |
| MOCK-E2E-08 | 零采样窗口、计数回退等计算失败，分别 enabled=true / false | 与探测失败区分原因；开启则重新计算后执行，关闭则终止 | TEST-RTE-18、21、23 |
| MOCK-E2E-09 | Pool NoCapacity → Accepted，分别开启 / 关闭延时 | 开启按间隔重试后执行一次，关闭不重试 | TEST-RTE-08–09 |
| MOCK-E2E-10 | 持续资源失败 / 队列满直至超时 | deadline 不延长，TASK_DELAY_EXPIRED 后无探测、提交或执行 | TEST-RTE-11、24 |
| MOCK-E2E-11 | 资源不足 → 探测失败 → 计算失败 → NoCapacity → 恢复 | 全过程只有一个待重试项，沿用首次 deadline，最终最多成功接收一次 | TEST-RTE-14–15、24 |
| MOCK-E2E-12 | 重试和 deadline 同时到达；检查开始前未超时、返回后已超时 | 到期优先，资源检查完成后也不得晚提交 | TEST-RTE-16、26 |
| MOCK-E2E-13 | 一个目标 Accepted，另一个延时后恢复 / 到期 | 已接收目标不重试；失败不阻断其他目标；最终集合准确 | TEST-RTE-27 |
| MOCK-E2E-14 | Pool Error、延时队列满、重复唤醒 | Error 不重试，满队列拒绝有日志，重复唤醒不重复接收 / 执行 | TEST-RTE-10、25 |
| MOCK-E2E-15 | 延迟消费并在通知返回后释放调用方元数据引用 | Key / SessionID / FunctionInfo / Args 保持正确，借用地址未被改写或释放 | TEST-TSK-07–08、API-05 |
| MOCK-E2E-16 | 多事件并发、多目标展开和输出回流 | 预期任务和最终结果集合一致，无额外接收、无数据竞争 | EVT-05、TSK-05、RTE-07 |
| MOCK-E2E-17 | 未初始化、非法事件、非法 app.json（含数值溢出 / 多 App 延时配置冲突）、Running 再初始化 | 错误语义正确，不产生意外任务；初始化失败无部分发布 | TEST-APP、TEST-API-12–15 |
| MOCK-E2E-18 | 停止事件源，存在延时项及已 Accepted 任务时退出 | 内部关闭流程结束，worker / timer / pending 无残留；只清理自有元数据 | 04 §14.6、API-05 |
| MOCK-E2E-19 | 多 App 同名函数、不同 Bucket；Bucket 冲突配置 | 正确 App 隔离；冲突拒绝且不破坏已加载配置 | TEST-APP-09、14 |
| MOCK-E2E-20 | 同一合法通知主动发送两次 | 按两次事件分别触发，不能把“重试不重复接收”误实现成通知去重 | EVT-01、EVT-05 |

每次测试的事件身份由夹具在外部记录。一次事件对同目标被不同规则合法命中时，按配置分别生成任务；不能据函数名合并。Accepted 后不再重试仅针对同一个已构建任务，不代表 SDK 重复通知会被去重。

## E.7 Linux、模糊测试与回归

| 类别 | 场景 | 通过条件 |
|---|---|---|
| ENV-01：原始样本 | cgroup v1 / v2、父层限额、无限额、cpuset / affinity、主机内存、文件缺失及非法数值 | 真实解析和计算结果与独立样本预期一致，异常不绕过限制 |
| ENV-02：目标环境 | 真实 Linux procfs / cgroup 的受控 CPU / Memory 限额和使用量；支持的 v1 / v2 环境分别执行 | 来源和有效限制正确；不要求动态统计值逐字相等，以受控边界及合理误差验证，不以机器负载碰运气 |
| CON-01：并发 | 并发通知、资源共享快照、Delay Queue、消费与退出交错 | 任务集合准确、无重复接收、无遗留 goroutine / timer / pending；race 无报告 |
| FUZZ-01：配置 | 非法 JSON、重复 / 未知字段、错误数值、引用及长度边界 | 无 panic、无部分配置发布；保留失败输入并加入回归 |
| FUZZ-02：事件与调度序列 | 空 / 边界参数、生成资源状态与 Pool 返回序列、时间推进 | 不解引用任意无效地址；只用夹具拥有的有效内存或合法 nil 场景，无重复提交或 deadline 延长 |
| REG-01：回归 | 修复过的故障和对应最小输入 | 每个修复有可重复失败的回归场景，后续提交自动执行 |

ENV-01 可独立执行，ENV-02 必须在目标 Linux 环境运行。缺少某种受支持环境时标为未执行，不得把文本夹具通过写成真实环境已验证；保留已完成的独立逻辑验收结果，并明确环境验收缺口。

## E.8 CI 与执行顺序

测试实现建议目录：tests/unit、tests/component、tests/mocke2e、tests/fixtures、tests/environment；测试辅助代码放在测试内部。当前目录及 runner 是实现目标，不代表已有可执行文件。

1. 每个 PR 执行格式检查、go vet、单元测试、内部链路和确定性 Mock E2E；不依赖真实 SDK / Pool。
2. Linux CI 执行 race 检查及目标环境资源测试。并发专项建议使用固定种子及 100 / 1000 事件、1 / 8 / 32 发送协程的组合，逐例保存输入数和预期扇出，不假定全局事件顺序。
3. PR 执行配置 / 事件模糊测试短任务（建议各 30 秒），定期回归延长；测试预算与种子写入报告。超预算或 CI 失败不能静默算通过。
4. 测试全局时钟与 Registry 不跨进程共享；依赖全局状态的用例不使用 t.Parallel。独立进程或独立私有实例才允许并行。
5. 发生 race、panic、错误接收次数、晚于 deadline 提交或进程退出后仍有工作项时，阻断验收。先修复或明确未通过，不靠盲目重跑消除失败。

Go 工具链、测试目录和 runner 就绪后，基础检查为 go test ./...、go vet ./...、go test -race ./...；模糊测试按具体测试函数运行并保存语料。工具链版本由项目构建环境统一锁定。本次文档更新不声明这些命令已通过。

## E.9 验收门槛与报告

### A：本模块独立验收

- P0 需求均映射到已执行的测试，正常、异常和边界分支通过；现有 88 项编号检查中，接口形态 / 范围检查标为静态契约检查，不混写成运行时测试。
- 真实模块的配置、匹配、任务构建、资源检查和延时队列链路通过；原始资源样本和目标 Linux 环境检查有单独结果。
- 并发 / race、清理和回归通过；没有未关闭的阻塞缺陷。缺失环境或未执行项明示，不以覆盖率百分比替代关键行为断言。

### B：Mock 端到端验收

- A 的逻辑与契约检查通过；Fake Pool 自检通过；MOCK-E2E-01–20 及对应参数化分支全部通过。
- 每个流水线核对最终字节结果，同时核对目标集合、字段、地址、接收 / 消费次数及错误 / 延时原因。按配置拒绝、延时队列满、Pool Error、超时、不命中、非法输入或主动退出的负向场景允许没有最终输出，必须断言对应终止 / 拒绝结果和任务次数。
- 成功、恢复后成功、按配置拒绝、到期、内部错误、退出等结果都有可检查证据。测试函数执行失败归测试环境 / 函数或 Pool 处理，不新增调度模块的执行重试功能。

### C：真实联调

SDK 与 Pool 接口版本对齐后，在真实环境验证 Commit 通知、异步描述符边界、来源函数注入、真实队列接收、唤醒及实际函数输入 / 输出。SDK 内部回收和 Pool 内部能力由各自团队提供证据；联合验证只评价本次接入链路。

报告必须包含：代码 commit、设计 / 契约版本、Go / OS / cgroup 环境、执行命令、用例 ID 与参数、输入 / 资源 / Pool 返回脚本、随机种子、预期与实际结果、日志、失败复现方式及未执行项。阶段状态使用“未执行 / 通过 / 失败 / 阻塞”，不得把尚未联调记作已通过。

| 阶段 | 本次文档修订时的状态 | 后续填写依据 |
|---|---|---|
| 文档静态一致性 | 以提交说明的实际检查为准 | 链接、编号、配置示例、需求映射检查 |
| A：本模块独立验收 | 未执行 | 实现和 CI / 环境报告 |
| B：Mock 端到端验收 | 未执行 | 替身自检、MOCK-E2E、最终输出及异常分支报告 |
| C：真实上下游联调 | 未执行，等待上下游集成 | 真实 SDK / Pool 与联合测试记录 |

## E.10 阶段报告模板

以下为填写模板，不是已经执行的报告；实际产物随实现提交保存，禁止填入未执行的通过结果。

| 项目 | 填写内容 |
|---|---|
| 阶段 / 总结论 | A / B / C；未执行 / 通过 / 失败 / 阻塞 |
| 固定版本 | 被测代码 commit、设计 / 契约版本、测试代码版本 |
| 环境 | Go、OS、内核、cgroup v1 / v2、真实资源限制、测试时钟种类 |
| 执行 | 命令、开始 / 结束时间、超时和模糊测试预算 |
| 用例明细 | ID、参数、预期与实际、结果、日志及输入输出证据路径 |
| 失败复现 | 夹具、资源 / Pool 脚本、时间推进顺序、种子、最小复现 |
| 未执行 / 跳过 | 原因、影响阶段与待补环境，不能纳入通过项 |
| 缺陷 | 阻塞缺陷、修复 commit、对应回归结果 |
| 职责边界 | Mock 已证明的行为、待真实联调项目及上下游负责项 |

---

<a id="part-12"></a>

## V0.6 代码阅读指引

第一次阅读时，建议跟踪一条数据通知从应用配置、规则匹配到任务入队的过程。先读下面的 1–4 节，再按需要深入 Linux 资源采集和测试。上游 SDK 管理数据和会话，触发引擎匹配规则并检查资源，下游任务池接收并执行任务。 [总体设计第 3 章](#part-1)

## 1. 从两个公共入口开始

先看 `TriggerInit` 和 `EventTriggerNotify`。前者在启动时注册应用，后者在 SDK 成功提交数据后接收通知。接着看 `DataReadyEvent`、`TaskInvocation` 和 `TaskPool.TryEnqueue`，确认通知携带哪些字段，以及任务池最终接收什么。最后看 `wiring.Install` 和 `engine.New`，找到任务池、资源采集器和时钟的装配位置。 [公共 API](../api/api.go) · [公共类型](../internal/contract/types.go) · [运行时装配](../internal/wiring/wiring.go) · [接口设计第 10–11 章](#part-3)

## 2. 跟踪应用配置如何注册

打开端到端测试的 `app.json`，先找 `functions`、`buckets`、`dependencies` 和 `triggers`。再沿 `Runtime.Init → config.Parse → Runtime.publish` 读代码。JSON 字段和引用校验先在临时配置中完成，跨应用冲突检查全部通过后才更新运行时索引。重点看数据桶名称如何跨应用保持唯一，以及为什么所有应用都要在首条有效通知之前注册。 [测试配置](../tests/mocke2e/testdata/app.json) · [配置解析](../internal/config/config.go) · [JSON 校验](../internal/config/json.go) · [运行时初始化](../internal/engine/engine.go) · [设计第 5 章](#part-2)

## 3. 跟踪一条通知如何生成任务

在 `Runtime.Notify` 里，先看 `beginNotify` 如何校验数据桶、来源函数和数据参数，再看直接依赖与数据桶规则的选择顺序。用同一份测试配置跟踪两条通知：

1. 向 `input` 发送数据。它匹配 `IMMEDIATE` 规则，生成函数 `A` 的任务。
2. `A` 提交 `output` 数据时，来源函数标识命中 `A → B` 的 `DIRECT` 依赖，因此生成 `B` 的任务并跳过 `output` 上触发 `C` 的规则。也可以用 `named` 数据桶对照 `BY_NAME`，它只匹配键 `ready`。

随后检查任务的 `Function`、`InputMode` 和单项 `Args`。每个命中项有独立的任务描述，`DataAddr` 指向的数据由上游 SDK 保持有效，直至相关任务结束。 [事件处理](../internal/engine/engine.go) · [端到端用例中的 pipeline 与 byName](../tests/mocke2e/scenarios_test.go) · [设计第 6–8 章](#part-2)

## 4. 跟踪入队、重试和关闭

按 `router.submit → attempt → TaskPool.TryEnqueue` 阅读首次提交路径，再按 `deferTask → schedule → run → attempt` 阅读重试路径。资源不足、采集失败或任务池暂时无容量时，由延时配置决定是否重试。队列中的任务保留首次延时确定的期限，每次重试重新检查资源。随后看 `Runtime.Close` 和 `router.close`，确认关闭如何阻止新通知、取消待处理任务并等待在途调用。 [任务路由和重试](../internal/engine/router.go) · [运行时关闭](../internal/engine/engine.go) · [工程设计第 14 章](#part-4)

## 5. 深入资源采集和测试

阅读资源代码时，先看 `Evaluate` 的输入和判定，再看 `Linux.Read` 如何构造快照。沿 `sampleBase → readBase → groups` 追踪 CPU、内存与 cgroup 限额。采样间隔和共享缓存位于 `sample.go`，GPU、磁盘和网卡的可选采集入口位于 `Linux.Read`。 [资源判定](../internal/resource/resource.go) · [Linux 采集](../internal/resource/linux.go) · [CPU 和内存](../internal/resource/base_linux.go) · [cgroup](../internal/resource/cgroup_linux.go) · [双采样与缓存](../internal/resource/sample.go)

| 要验证的问题 | 先读的测试 |
|---|---|
| 配置何时被拒绝，失败是否影响已注册应用 | `TestConfig`、`TestInitialization`、`TestPublication` |
| 直接依赖与数据桶规则如何选择，任务池何时接收 | `TestMockAcceptance` 下的 `pipeline`、`byName` 和 `fanOut` |
| 复杂配置下的路由优先级、同一 Bucket 的不同来源及多 App 隔离 | `TestComplexConfigRouting`，100 个生成的 app.json 配置场景 |
| 多跳数据链路、扇出及故障恢复 | `TestComplexDataDriven`，100 个复杂流程场景 |
| 资源何时恢复，重试何时结束 | `TestMockAcceptance` 下的 `recoverResources`、`expiry` 和 `deadlineDuringProbe` |
| cgroup 与可选资源如何计算，真实 Linux 能读到什么 | `TestLinuxResourceCompatibility`、`TestAdditionalResources`、`TestLinuxEnvironment` |

在仓库根目录先运行端到端用例，随后运行全部 Go 测试。格式检查、构建、静态检查、竞态检测和模糊测试的完整命令见 GitHub Actions 工作流。 [配置测试](../tests/config/config_test.go) · [运行时测试](../tests/engine/engine_test.go) · [资源测试](../tests/resource/linux_test.go) · [CI 工作流](../.github/workflows/go.yml)

```sh
go test -count=1 -v ./tests/mocke2e -run '^TestMockAcceptance$/^MOCK-E2E-01$'
go test -count=1 ./...
```
