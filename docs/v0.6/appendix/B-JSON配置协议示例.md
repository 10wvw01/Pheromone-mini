# 附录 B：app.json / function.json 配置协议与示例

## B.1 配置原则

V0.6 保留两类 JSON 配置文件，职责分离：

- app.json：App 级配置，描述 App 元信息、Function 配置文件列表、Bucket、DIRECT Dependency、Trigger 和全局 Delay Queue。
- function.json：单 Function 配置文件，描述该 Function 的 so_name、func_name、版本和资源需求。

TriggerInit 只接收 app.json 文件路径。模块自行读取 app.json，并根据 functions[].config_path 加载所有 function.json。function.json 的相对路径以 app.json 所在目录为基准。

app.json 是应用拓扑和调度规则的唯一来源；DIRECT 和 Bucket Trigger 关系只写在 app.json，不在 function.json 重复声明，避免双重事实来源。function.json 当前只定义 CPU / Memory Resource，不定义未经确认的 execution 配置。

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
    { "config_path": "functions/producer/function.json" },
    { "config_path": "functions/processor/function.json" },
    { "config_path": "functions/direct_source/function.json" },
    { "config_path": "functions/direct_processor/function.json" }
  ],
  "buckets": ["b_input", "b_shared", "b_direct_output", "b_result"],
  "dependencies": [
    {
      "type": "DIRECT",
      "src_functions": ["direct_source"],
      "tgt_functions": ["direct_processor"]
    }
  ],
  "triggers": [
    {
      "bucket": "b_input",
      "trigger_name": "t_input_immediate",
      "primitive_type": "IMMEDIATE",
      "primitive": { "function": "producer" },
      "trigger_option": 0
    },
    {
      "bucket": "b_shared",
      "trigger_name": "t_shared_immediate",
      "primitive_type": "IMMEDIATE",
      "primitive": { "function": "processor" },
      "trigger_option": 0
    },
    {
      "bucket": "b_shared",
      "trigger_name": "t_shared_byname_ready",
      "primitive_type": "BY_NAME",
      "primitive": { "function": "processor", "key_name": "ready" },
      "trigger_option": 1
    }
  ]
}
~~~

## B.3 function.json 单函数示例

文件路径：functions/producer/function.json

~~~json
{
  "so_name": "producer.so",
  "func_name": "producer",
  "description": "处理输入并产生普通 Bucket 数据",
  "version": "1.0.0",
  "resource": {
    "cpu_cores": 1,
    "memory_mb": 128
  }
}
~~~

其他 Function 使用相同结构，各自维护独立 function.json。示例目录提供 producer、processor、direct_source、direct_processor 四份配置。

## B.4 字段定义

### app.json 顶层

| 字段 | 类型 | 约束 |
|---|---|---|
| app_name | string | 必填；Registry 内唯一 |
| description | string | 可选 |
| version | string | 必填 |
| scheduler.delay_queue.enabled | boolean | 必填 |
| scheduler.delay_queue.retry_interval_ms | integer | 必填且大于 0 |
| scheduler.delay_queue.max_wait_ms | integer | 必填且大于 0，且不小于 retry_interval_ms |
| functions | array(object) | 必填，至少一个 |
| functions[].config_path | string | 必填；相对 app.json 所在目录；不得路径穿越 |
| buckets | array(string) | 必填；跨 App 全局唯一 |
| dependencies | array(object) | 可为空；仅 DIRECT |
| triggers | array(object) | 可为空；仅 IMMEDIATE / BY_NAME |

### function.json

| 字段 | 类型 | 约束 |
|---|---|---|
| so_name | string | 必填；安全 SO 文件名，不允许路径分隔符 |
| func_name | string | 必填；当前 App 内唯一 |
| description | string | 可选 |
| version | string | 可选 |
| resource.cpu_cores | number | 必填；有限且大于 0 |
| resource.memory_mb | integer | 必填；大于 0 |

function.json 不包含 execution、timeout_ms、GPU、Disk、Network 等当前未纳入本期资源准入范围的字段。

### DIRECT Dependency

| 字段 | 类型 | 约束 |
|---|---|---|
| type | string | 仅 DIRECT |
| src_functions | array(string) | 必填；恰好一个来源 func_name |
| tgt_functions | array(string) | 必填；至少一个目标 func_name |

来源和目标必须属于同一 App 的 functions。一个来源可对应多个目标；多个来源拆成多条依赖配置。

### Bucket 与 Trigger

| 字段 | 类型 | 约束 |
|---|---|---|
| buckets[] | string | 当前 Registry 全局唯一 |
| bucket | string | Trigger 必填，必须在本 App buckets 中 |
| trigger_name | string | App 内唯一 |
| primitive_type | string | IMMEDIATE / BY_NAME |
| primitive.function | string | 目标 func_name，必须已注册 |
| primitive.key_name | string | BY_NAME 必填；IMMEDIATE 禁止 |
| trigger_option | integer | 0 / 1 / 2；默认 0 |

trigger_option：0=Value，1=Key+Value，2=Key Only。DIRECT 不使用 trigger_option，固定以 Value 作为目标输入。

## B.5 校验规则

1. 拒绝非法 JSON、重复字段、未知字段、错误类型和超长输入。
2. app_name 在 Registry 内唯一；Bucket 跨全部 App 全局唯一。
3. functions[].config_path 必须是非空相对路径，解析后不得逃逸出 app.json 配置根目录。
4. 每个 function.json 必须可读取且结构合法；so_name、func_name 必填。
5. func_name 在当前 App 内唯一；so_name 可以被多个 Function 共享。
6. 同一 so_name + func_name 被多个 App 复用时，Resource 必须一致。
7. 每条 DIRECT 配置必须只有一个 src_functions 项，tgt_functions 至少一个；来源和目标都必须存在。
8. Trigger 的 Bucket 和目标 func_name 必须存在；trigger_name 在 App 内唯一。
9. 仅接受 IMMEDIATE / BY_NAME；ByName 必须配置非空 key_name，Immediate 禁止 key_name。
10. trigger_option 只允许 0、1、2。
11. Delay Queue enabled 必须为布尔值；时间间隔和最大等待时间必须为正整数。
12. 拒绝 execution、resource.timeout_ms、DataRef 和旧的双数据载体字段。
13. 当前 App 的全部配置必须整体校验成功后才发布；任一 function.json 失败不得留下部分 AppRecord。
