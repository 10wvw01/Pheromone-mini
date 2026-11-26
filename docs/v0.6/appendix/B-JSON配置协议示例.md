# 附录 B：app.json 配置协议与示例

## B.1 配置原则

V0.6 仅使用 app.json。用户根据本文字段说明和示例自行完成编写，再将文件路径传入 TriggerInit；模块自行读取、解析和校验，不负责生成或自动补写业务配置。

app.json 包含 App 元信息、内嵌 functions[]、Bucket、DIRECT、Trigger 和 Delay Queue。每项 Function 包含 so_name、func_name、description、version、resource；CPU / Memory 是本期必做准入维度，GPU / Disk / Network 保留为透传元数据。全部初始化成功结束后才允许发送 EventTriggerNotify。

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
      ]
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
2. 在 functions[] 逐项填写 SO 名、函数名和资源需求；函数名在当前 App 内唯一。
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
| scheduler.delay_queue.enabled | boolean | 必填 |
| scheduler.delay_queue.retry_interval_ms | integer | 必填且大于 0 |
| scheduler.delay_queue.max_wait_ms | integer | 必填且大于 0，且不小于 retry_interval_ms |
| functions | array(object) | 必填，至少一个 |
| functions[] | object | 直接包含下表 Function 字段；不接受外部配置路径 |
| buckets | array(string) | 必填；跨 App 全局唯一 |
| dependencies | array(object) | 可为空；仅 DIRECT |
| triggers | array(object) | 可为空；仅 IMMEDIATE / BY_NAME |

### functions[] 每项字段（下列路径均相对该项）

| 字段 | 类型 | 约束 |
|---|---|---|
| so_name | string | 必填；安全 SO 文件名，不允许路径分隔符 |
| func_name | string | 必填；当前 App 内唯一 |
| description | string | 可选 |
| version | string | 可选 |
| resource.cpu_cores | number | 必填；有限且大于 0 |
| resource.memory_mb | integer | 必填；大于 0 |
| resource.gpu.required | boolean | 可选；默认 false |
| resource.gpu.device_count | integer | 可选；默认 0；非负 |
| resource.gpu.memory_mb | integer | 可选；默认 0；非负 |
| resource.disk_mb | integer | 可选；默认 0；非负 |
| resource.network_mbps | integer | 可选；默认 0；非负 |

Function 配置不包含 execution、timeout_ms；GPU、Disk、Network 字段保留为静态资源元数据，当前不参与 Task Router 的 CPU / Memory 准入判断。

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
3. functions[] 必须是非空对象数组，Function 字段直接内嵌。
4. 每项 Function 结构必须合法，so_name、func_name 必填；拒绝外部配置路径。
5. func_name 在当前 App 内唯一；so_name 可以被多个 Function 共享。
6. 同一 so_name + func_name 被多个 App 复用时，Resource 必须一致。
7. 每条 DIRECT 配置必须只有一个 src_functions 项，tgt_functions 至少一个；来源和目标都必须存在。
8. Trigger 的 Bucket 和目标 func_name 必须存在；trigger_name 在 App 内唯一。
9. 仅接受 IMMEDIATE / BY_NAME；ByName 必须配置非空 key_name，Immediate 禁止 key_name。
10. trigger_option 只允许 0、1、2。
11. Delay Queue enabled 必须为布尔值；时间间隔和最大等待时间必须为正整数。
12. 拒绝 execution、resource.timeout_ms、DataRef 和旧的双数据载体字段；Resource 中 GPU、Disk、Network 数值必须非负。
13. 当前 App 的全部配置必须整体校验成功后才发布；任一内嵌 Function 校验失败不得留下部分 AppRecord。
