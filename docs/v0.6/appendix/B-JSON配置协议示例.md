# 附录 B：app.json 配置字段规范与使用示例（V0.6）

[返回 V0.6 主文档](../../V0.6-完整软件设计方案.md) · [实际 app.json](../examples/app.json)

> 本文是唯一 app.json 配置协议的逐字段参考，面向 App 配置编写者、App Registry、Event Trigger、Task Router 和 Task Coroutine Pool 研发人员。字段含义、校验和实际消费方分开描述；静态声明不等于已实现资源隔离能力。

## B.1 配置结构与生效阶段

每个 App 使用一份 app.json，由框架在启动期调用 register_app(registry, config_json, config_size) 注入。App Registry 统一解析、校验和发布索引；seal 后配置只读。多个 App 可以分别注册，但共享 Registry 范围内的 Bucket 名称空间及进程级 Delay Queue 配置。

| 一级字段 | 职责 | 生效模块 |
|---|---|---|
| app_name / description / version | 应用身份及描述 | App Registry |
| scheduler | 本地容量不足后的等待策略 | Task Router |
| functions | 声明可执行 SO 和静态配置 | App Registry、Task Coroutine Pool |
| buckets | 声明可用于关联 Trigger 的 Bucket 名称 | App Registry、Event Trigger |
| dependencies | DIRECT 函数依赖 | DIRECT Dependency |
| triggers | Bucket 对象到达后的触发规则 | Event Trigger、Task Builder |

字段路径中的 [] 表示数组元素；类型采用 JSON 类型。表中“待确认”表示当前设计未冻结具体缺省行为或下游执行语义，不应自行推断。示例值不等于默认值。

## B.2 字段字典

### B.2.1 App 顶层字段

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| app_name | string | 必填 | Registry 内全局唯一；smc_demo | App 注册索引的 KEY；不是进程名、SO 名或业务 Session；不进入 DataReadyEvent / TaskInvocation | App Registry |
| description | string | 可选；无强制默认值 | 单节点单进程数据驱动示例 | 人工阅读和诊断用描述；不参与匹配、调度或资源准入 | App Registry |
| version | string | 必填 | 0.6.0 | App 配置版本标识；不是 SO 版本，不表示支持热更新或版本路由 | App Registry |
| scheduler | object | 必填 | 包含 delay_queue | 全局调度策略容器 | App Registry、Task Router |
| functions | array(object) | 必填 | 至少一个 Function | 本 App 允许引用的 SO 及静态执行配置集合 | App Registry、Pool |
| buckets | array(string) | 必填 | b_input、b_shared 等 | 本 App 声明的 Bucket 名称集合；不是共享内存创建或容量配置 | App Registry |
| dependencies | array(object) | 可为空数组；省略是否允许待确认 | 仅 DIRECT | 配置来源函数与目标函数的直接依赖关系 | DIRECT Dependency |
| triggers | array(object) | 可为空数组；省略是否允许待确认 | 仅 IMMEDIATE / BY_NAME | 配置 Bucket 对象到达后需要执行的目标函数及参数模式 | Event Trigger |

### B.2.2 scheduler：全局延时调度

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| scheduler.delay_queue | object | 必填 | 下列三个字段 | 进程级本地延时队列策略；不是 Function 级策略 | Task Router |
| scheduler.delay_queue.enabled | boolean | 必填；无默认 | true / false | Pool 返回 NO_CAPACITY 时，true 表示允许进入 Delay Queue；false 表示记录 PROCESS_DISPATCH_REQUIRED、dispatched=false 并释放任务 | Task Router |
| scheduler.delay_queue.retry_interval_ms | integer | 必填；无默认 | 正整数，示例 10 ms | 未入队任务的重新提交间隔；不是 handler 执行间隔，也不是忙轮询周期 | Delay Queue |
| scheduler.delay_queue.max_wait_ms | integer | 必填；无默认 | 正整数，且不小于 retry_interval_ms；示例 1000 ms | 从首次 NO_CAPACITY 开始计算的最大等待时间；到期记录 TASK_DELAY_EXPIRED 并释放任务 | Delay Queue |

三个字段必须在所有 App 中完全一致。首个成功注册的 App 建立进程级配置；后续不一致时返回 PM_ERR_GLOBAL_SCHEDULER_CONFIG_CONFLICT。即使 enabled=false，现有协议仍要求另外两个字段合法。时间使用单调时钟；示例 10 / 1000 不是固定默认值，也不保证恰好重试 100 次。已入队或执行过的任务不会因本配置重新执行。

### B.2.3 functions：函数身份与静态元数据

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| functions[] | object | 每个数组元素必需 | 一个 Function 声明 | 绑定一个物理 SO 及其静态执行配置 | App Registry |
| functions[].so_name | string | 必填 | producer.so；安全文件名，禁止绝对路径、路径分隔符与路径穿越 | Function 执行标识；Pool 定位 /dev/shm 下该 SO 并调用固定 handler；也是 DIRECT / Trigger 的引用目标 | App Registry、Task Builder、Pool |
| functions[].description | string | 可选；无强制默认值 | 处理输入并产生普通 Bucket 数据 | 说明函数业务职责；不参与执行决策 | App Registry |
| functions[].version | string | 可选；无强制默认值 | 1.0.0 | 函数版本元数据；当前不参与 SO 路径选择、动态升级或多版本路由 | App Registry |
| functions[].resource | object | 必填（含必填子字段） | 资源声明 | 注册阶段交给 Pool 的静态资源配置；不是实时资源占用量 | App Registry、Pool |
| functions[].execution | object | 子字段可选；整体省略规则待确认 | 执行声明 | 注册阶段交给 Pool 的静态执行配置；不是每个 Task 的动态参数 | App Registry、Pool |

同一 App 内 so_name 唯一。不同 App 复用同一物理 so_name 时，resource / execution 必须一致。Function 无状态、入口固定 handler，因此不需要再配置 entry_name、handler_name 或 SO 绝对路径。

### B.2.4 functions[].resource：资源需求声明

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| functions[].resource.cpu_cores | number | 必填 | 有限且大于 0；1 | 声明 Function 所需 CPU 核资源；不是 CPU ID，也不代表固定绑一个核 | Pool |
| functions[].resource.memory_mb | integer | 必填 | 大于 0；128 | 声明 Function 所需内存大小，单位 MB；不是 Object 数据大小 | Pool |
| functions[].resource.gpu | object | 子字段可选；整体省略规则待确认 | GPU 资源声明容器 | 表达是否需要 GPU 及相关资源需求 | Pool |
| functions[].resource.gpu.required | boolean | 可选；false | true / false | 声明是否需要 GPU | Pool |
| functions[].resource.gpu.device_count | integer | 可选；0 | 非负；0 | 声明需要的 GPU 设备数量，不是设备 ID | Pool |
| functions[].resource.gpu.memory_mb | integer | 可选；0 | 非负；0 | 声明 GPU 显存需求，单位 MB；按设备还是合计需下游确认 | Pool |
| functions[].resource.disk_mb | integer | 可选；0 | 非负；0 | 磁盘资源需求声明，单位 MB；0 表示无显式需求；不授予业务文件 I/O 权限 | Pool |
| functions[].resource.network_mbps | integer | 可选；0 | 非负；0 | 网络带宽需求声明，单位 Mbps；0 表示无显式需求；不授予业务 Socket 通信权限 | Pool |

注意：这些字段由 App Registry 解析、校验并在静态 Function 注册时一次性交给 Pool；本模块不负责 CPU/GPU 分配、限额隔离、磁盘或网络带宽保障。V0.6 的运行时准入以 Pool 的原子 try_enqueue 返回值为准，不由 Router 重复计算资源余量。具体哪些资源维度实际参与准入，以及 MB 的字节换算、GPU 字段联动约束，需 Pool 团队评审确认。resource.timeout_ms 已删除，不允许配置。

### B.2.5 functions[].execution：执行策略声明

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| functions[].execution.type | string | 可选；默认值未冻结 | 示例 coroutine | 声明执行方式；本期执行器为协程池，其他取值是否允许由下游确认 | Pool |
| functions[].execution.priority | integer | 可选；0 | int32_t 可表示范围；0 | 调度优先级声明；数值越大是否越优先、是否支持优先级队列由 Pool 确认 | Pool |
| functions[].execution.affinity | array(integer) | 可选；[] | uint32_t 可表示的非负 ID | 执行资源亲和性声明；空数组表示未指定；ID 命名空间、绑核或亲和实现由 Pool 确认 | Pool |
| functions[].execution.max_concurrency | integer | 可选；0 表示未指定 | uint32_t 可表示的非负整数 | Function 最大并发数声明；正数表示指定上限，0 不代表禁止执行，也不承诺无限并发 | Pool |
| functions[].execution.reuse | boolean | 可选；false | true / false | 是否允许下游复用执行资源的声明；具体复用对象和行为由 Pool 确认，不代表本模块实现冷热 Executor 分发 | Pool |

resource / execution 均为静态元数据，不重复放入 TaskInvocation。配置字段存在不代表 Pool 已实现对应策略。affinity_count 由解析后的数组长度派生，不是 app.json 字段。

### B.2.6 buckets：Bucket 声明

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| buckets[] | string | 每个数组元素必需 | b_shared；Registry 全部 App 内全局唯一 | 数据对象所属逻辑名称空间；建立 Bucket → App → Trigger[] 索引 | App Registry、Event Trigger |

Bucket 可不配置任何 Trigger（如示例 b_result）；合法通知到达时若无 DIRECT 依赖也无命中规则，则不生成任务。这里仅声明名称和关联关系，不创建 Data SDK 底层 Bucket，也不配置存储大小或共享内存池。DataReadyEvent 不需要 app_name，依据 Bucket 反查所属 App。

### B.2.7 dependencies：DIRECT 函数依赖

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| dependencies[] | object | 每个数组元素必需 | 一条依赖关系 | 建立来源函数到目标函数的直接执行关系 | App Registry |
| dependencies[].type | string | 必填 | 仅 DIRECT | 依赖类型；独立于 Bucket Trigger Primitive | DIRECT Dependency |
| dependencies[].src_functions | array(string) | 必填 | 恰好一个；["direct_source.so"] | 来源 SO；与 DataReadyEvent.source_function 匹配；必须引用本 App 的 functions | DIRECT Dependency |
| dependencies[].src_functions[] | string | 元素必需 | direct_source.so | 已注册的来源 SO 文件名 | DIRECT Dependency |
| dependencies[].tgt_functions | array(string) | 必填 | 至少一个；["direct_processor.so"] | 目标 SO 集合；每个目标生成独立 Task；必须引用本 App 的 functions | DIRECT Dependency |
| dependencies[].tgt_functions[] | string | 元素必需 | direct_processor.so | 已注册的目标 SO 文件名 | Task Builder |

DIRECT 不是“某个 Bucket 到达即触发”。它依据来源 Function 匹配。来源函数有 DIRECT 目标时，本次事件只走 DIRECT，不再匹配 Bucket Trigger；不存在 DIRECT 时才进入正常 Bucket Trigger。DIRECT 固定以本次提交对象的 Value 作为输入，不支持 trigger_option。多个来源需要分别配置依赖条目。

### B.2.8 triggers：Bucket Trigger 规则

| 字段路径 | JSON 类型 | 必填 / 默认 | 约束与示例 | 字段含义及实际作用 | 消费方 |
|---|---|---|---|---|---|
| triggers[] | object | 每个数组元素必需 | 一条独立规则 | 定义一个 Bucket 上的数据触发条件、目标函数和输入方式 | App Registry |
| triggers[].bucket | string | 必填 | b_shared；必须在本 App buckets 中 | 关联数据所属 Bucket；用于查找规则列表 | Event Trigger |
| triggers[].trigger_name | string | 必填 | t_shared_immediate；App 内唯一 | 规则标识，用于配置索引、日志和诊断；不传给 Handler | App Registry、Event Trigger |
| triggers[].primitive_type | string | 必填 | IMMEDIATE / BY_NAME | 决定数据到达后如何判断规则命中 | Event Trigger |
| triggers[].primitive | object | 必填 | 包含 function；BY_NAME 另含 key_name | Primitive 特有的规则参数 | Event Trigger |
| triggers[].primitive.function | string | 必填 | processor.so；本 App 已注册 SO | 命中时执行的目标 Function | Task Builder |
| triggers[].primitive.key_name | string | BY_NAME 必填；IMMEDIATE 禁止 | 非空；ready | 仅 ByName 使用；与事件 Key 完全相等、区分大小写才命中 | Event Trigger |
| triggers[].trigger_option | integer | 可选；默认 0 | 0 / 1 / 2 | 决定目标 Handler 接收 Value、Key+Value 还是 Key Only；不决定触发条件或数据载体 | Task Builder |

Primitive 匹配规则：

| primitive_type | 命中条件 | 不命中举例 |
|---|---|---|
| IMMEDIATE | 关联 Bucket 收到合法显式 Commit 通知，且未走 DIRECT | 其他 Bucket；没有通知的覆盖写 |
| BY_NAME | 上述条件成立，且 event.key 与 key_name 完全相同 | ready 配置下，Ready 或 ready_1 不匹配 |

同一 Bucket 可挂多条规则，逐条匹配、独立生成 Task；即使两条规则指向相同 SO 也不去重。

trigger_option 的实际输入语义：

| 配置值 | C 输入模式 | Handler 接收 | Inline | Shared-Memory |
|---:|---|---|---|---|
| 0 | PM_INPUT_VALUE | Value | 复制 Value | 每个 Task retain 一份 DataRef |
| 1 | PM_INPUT_KEY_VALUE | Key + Value | 复制 Key 和 Value | 复制 Key、retain 一份 DataRef |
| 2 | PM_INPUT_KEY_ONLY | Key | 不复制 Value | 不 retain DataRef |

例如事件 Key=ready、Value=42：option=0 只收到 42；option=1 收到 ready 和 42；option=2 只收到 ready。此处 42 仅用于说明业务数据，实际 Value 是字节缓冲区或 DataRef 所指向的数据。

Payload Type 由 Data SDK 决定（约 1KB 阈值由 SDK 负责），与 trigger_option 完全独立；不能用 trigger_option 选择共享内存。

## B.3 完整 app.json 示例

下面内容与 [examples/app.json](../examples/app.json) 一致，不包含 JSON 注释。

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
      },
      "execution": {
        "type": "coroutine",
        "priority": 0,
        "affinity": [],
        "max_concurrency": 0,
        "reuse": false
      }
    },
    {
      "so_name": "processor.so",
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
      },
      "execution": {
        "type": "coroutine",
        "priority": 0,
        "affinity": [],
        "max_concurrency": 0,
        "reuse": false
      }
    },
    {
      "so_name": "direct_source.so",
      "description": "DIRECT 依赖来源函数",
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
      },
      "execution": {
        "type": "coroutine",
        "priority": 0,
        "affinity": [],
        "max_concurrency": 0,
        "reuse": false
      }
    },
    {
      "so_name": "direct_processor.so",
      "description": "DIRECT 依赖目标函数",
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
      },
      "execution": {
        "type": "coroutine",
        "priority": 0,
        "affinity": [],
        "max_concurrency": 0,
        "reuse": false
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
      "src_functions": ["direct_source.so"],
      "tgt_functions": ["direct_processor.so"]
    }
  ],
  "triggers": [
    {
      "bucket": "b_input",
      "trigger_name": "t_input_immediate",
      "primitive_type": "IMMEDIATE",
      "primitive": {
        "function": "producer.so"
      },
      "trigger_option": 0
    },
    {
      "bucket": "b_shared",
      "trigger_name": "t_shared_immediate",
      "primitive_type": "IMMEDIATE",
      "primitive": {
        "function": "processor.so"
      },
      "trigger_option": 0
    },
    {
      "bucket": "b_shared",
      "trigger_name": "t_shared_byname_ready",
      "primitive_type": "BY_NAME",
      "primitive": {
        "function": "processor.so",
        "key_name": "ready"
      },
      "trigger_option": 1
    }
  ]
}
~~~

## B.4 结合示例理解运行时行为

### B.4.1 普通 Bucket Trigger

~~~mermaid
flowchart TD
    A["外部 Actor Commit：b_input / 任意 Key"] --> B["Immediate：producer.so"]
    B --> C["producer Commit：b_shared / ready"]
    C --> D["Immediate：processor.so，VALUE"]
    C --> E["ByName：processor.so，KEY_VALUE"]
    D --> F["各自调用 try_enqueue"]
    E --> F
~~~

外部 Actor 在 b_input 提交数据后，Immediate 产生 producer.so 任务。producer.so 写入 b_shared/ready 时，因其无 DIRECT 依赖，两条 Bucket Trigger 都命中，产生两个独立 processor.so 任务。若 Key 改为 other，则只命中 Immediate，产生一个任务。两个任务可能并发执行，完成顺序不由 Trigger 保证。

### B.4.2 DIRECT

当 direct_source.so 已通过业务流程被执行，并成功 Commit b_direct_output/direct_result 时，SDK 注入 source_function=direct_source.so。App Registry 在所属 App 中找到 DIRECT 依赖，生成 direct_processor.so 的 VALUE 任务。本次事件不进入 Bucket Trigger 匹配。当前完整示例没有配置启动 direct_source.so 的 Trigger；DIRECT 示例的来源任务需要由测试驱动等既有调用路径启动，此处不新增启动 API。

若只由外部 Actor 写入 b_direct_output，source_function=NULL，则不会命中上述 DIRECT；示例中该 Bucket 没有普通 Trigger，因此不产生任务。

### B.4.3 延时调度

示例 enabled=true、retry_interval_ms=10、max_wait_ms=1000。Task Router 第一次提交若被 ACCEPTED，直接交给 Pool；若返回 NO_CAPACITY，则 Router 持有该任务和 DataRef，到达重试时机再尝试提交，超过最大等待时间则记录 TASK_DELAY_EXPIRED 并释放。若 enabled=false，则第一次 NO_CAPACITY 后直接记录 PROCESS_DISPATCH_REQUIRED、dispatched=false 并释放。不做 Rerun，也不调用远程调度。

## B.5 JSON 到 C 接口与索引的映射

| 配置 | 注册时生成或传递 | 运行时用途 |
|---|---|---|
| app_name、description、version | AppRecord | App 定位及配置诊断 |
| scheduler.delay_queue | 进程级 Delay Queue 配置 | Router 决定暂存、重试和等待期限 |
| functions[].so_name | Function Index；pm_function_runtime_config_t.so_name | 目标解析、TaskInvocation.so_name |
| functions[].resource | pm_function_resource_t | 注册给 Pool，不重复传入 Task |
| functions[].resource.gpu.* | gpu_required / gpu_device_count / gpu_memory_mb | JSON 嵌套字段展开为 C 静态结构成员 |
| functions[].execution | pm_function_execution_t | 注册给 Pool；affinity_count 从数组派生 |
| buckets | Global Bucket Index | DataReadyEvent.bucket → owning App |
| dependencies | 所属 App 内的 DIRECT Index | (owning App, source_function) → target SO 列表 |
| triggers | Bucket Trigger Index | Primitive 匹配、目标 SO 和 Input Mode 解析 |
| triggers[].trigger_option | 内部输入模式配置 | pm_task_invocation_t.input_mode |

session_id、实际对象 Key（非配置的 key_name）、Payload Type、data_size、Inline / DataRef 由运行时 DataReadyEvent 提供，不是 app.json 静态字段。TaskInvocation 不包含 app_name、Trigger 名称、resource / execution 或入口名。

API 规格参见 [第三部分：API 接口规范](../03-接口与协作设计.md)，C 类型参见 [附录 C](C-C接口与Task-ABI.md)。

## B.6 配置校验与错误示例

| 校验内容 | 错误示例 | 处理 |
|---|---|---|
| JSON 格式、字段和类型 | 注释、重复字段、未知字段、enabled="true" | 拒绝非法配置 |
| App 身份 | 同名 app_name | PM_ERR_DUPLICATE_APP |
| Bucket 名称 | 两个 App 都声明 b_shared | PM_ERR_DUPLICATE_BUCKET |
| SO 名称 | ../producer.so | 拒绝非法配置 |
| Function 静态配置冲突 | 同一 so_name 对应不同资源声明 | 拒绝注册；Pool 静态注册冲突按接口规范处理 |
| DIRECT 关系 | src_functions 多于一个，或目标 SO 未声明 | 拒绝非法依赖或未知 Function |
| Trigger 引用 | 未声明 Bucket 或目标 SO | 拒绝非法 Trigger / 未知引用 |
| Primitive 参数 | BY_NAME 缺少 key_name；IMMEDIATE 含 key_name | PM_ERR_INVALID_TRIGGER |
| 输入模式 | trigger_option=3 | PM_ERR_INVALID_TRIGGER |
| 延时策略 | retry_interval_ms=0，或 max_wait_ms 小于重试间隔 | 拒绝非法配置 |
| 多 App 延时策略 | 后注册 App 改用不同 retry_interval_ms | PM_ERR_GLOBAL_SCHEDULER_CONFIG_CONFLICT |
| 已删除能力 | resource.timeout_ms、额外 function.json、其他依赖或 Primitive 类型 | 不支持，不纳入当前配置协议 |

还需校验资源值、整型范围、字符串及数组长度和整数转换溢出。所有配置校验及下游静态 Function 注册成功后才发布 App 索引；失败不能留下部分配置。详细状态码参见 [附录 D](D-数据结构与错误码.md)。

## B.7 尚需联合确认的配置语义

以下内容在原 V0.6 设计中未完全确定，本文不新增强制策略：

- execution.type 的缺省值及支持取值范围；execution / resource.gpu 对象是否允许整体省略；dependencies / triggers 省略与空数组是否等价。
- Pool 对 CPU、内存、GPU、磁盘、带宽声明的实际准入维度与隔离能力；GPU 资源字段的组合规则及 MB 字节换算。
- priority 的排序方向、affinity 的 ID 含义、max_concurrency 的实际限流方式、reuse 的具体复用对象。
- 字符串及数组最大长度、队列实现级容量等工程上限。

这些属于接口联调与实现规格待确认项，不是本模块新增功能，不影响现有完整 app.json 示例作为配置参考。V0.6 仍只使用单一 app.json，保持 DIRECT、Immediate / ByName、原子 try_enqueue 和全局 Delay Queue 设计不变。
