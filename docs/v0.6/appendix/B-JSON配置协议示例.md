# 附录 B：单一 app.json 配置协议与示例

## B.1 配置原则

V0.6 仅保留 app.json。该文件统一描述 App、Function、DIRECT Dependency、Bucket、Trigger 和全局 Delay Queue。scheduler.delay_queue 是进程级单例配置：首个成功注册的 App 建立该配置，后续 App 必须重复声明完全相同的三个字段；不一致时注册失败。

- 不使用 function.json。
- app_name 是 App Registry 全局唯一注册 KEY。
- Bucket 名称在当前 Registry 的所有 App 之间全局唯一。
- so_name 对应 /dev/shm 下的物理 SO 文件；固定导出 handler。
- DIRECT 使用 dependencies，且 type 仅接受 DIRECT。
- Trigger Primitive 仅接受 IMMEDIATE / BY_NAME。
- Delay Queue 是全局配置，不支持 Function 级单独配置。
- 不定义 resource.timeout_ms。

## B.2 完整示例

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

## B.3 字段定义

### App 顶层

| 字段 | 类型 | 约束 |
|---|---|---|
| app_name | string | 必填，Registry 全局唯一 |
| description | string | 可选 |
| version | string | 必填 |
| scheduler.delay_queue.enabled | boolean | 必填 |
| scheduler.delay_queue.retry_interval_ms | integer | 必填且大于 0 |
| scheduler.delay_queue.max_wait_ms | integer | 必填且大于 0，且不小于 retry_interval_ms |

三个 scheduler.delay_queue 字段在进程级全局生效。多个 App 的 app.json 必须声明相同配置；不允许每个 App 单独设置不同的 Delay Queue 策略。
| functions | array | 必填，至少一个 |
| buckets | array(string) | 必填；跨 App 全局唯一 |
| dependencies | array(object) | 可为空；仅 DIRECT |
| triggers | array(object) | 可为空；仅 IMMEDIATE / BY_NAME |

### Function

| 字段 | 类型 | 约束 |
|---|---|---|
| so_name | string | 必填，安全 SO 文件名 |
| description | string | 可选 |
| version | string | 可选 |
| resource.cpu_cores | number | 必填，有限且大于 0 |
| resource.memory_mb | integer | 必填，大于 0 |
| resource.gpu.required | boolean | 可选，默认 false |
| resource.gpu.device_count | integer | 可选，默认 0 |
| resource.gpu.memory_mb | integer | 可选，默认 0 |
| resource.disk_mb | integer | 可选，默认 0 |
| resource.network_mbps | integer | 可选，默认 0 |
| execution.type | string | 可选，例如 coroutine |
| execution.priority | integer | 可选，默认 0 |
| execution.affinity | array(integer) | 可选，默认空 |
| execution.max_concurrency | integer | 可选，0 表示未指定 |
| execution.reuse | boolean | 可选，默认 false |

资源和执行配置在注册阶段一次性传给 Pool，不在每个 Task 重复传递。没有执行超时字段。

### DIRECT Dependency

| 字段 | 类型 | 约束 |
|---|---|---|
| type | string | 仅 DIRECT |
| src_functions | array(string) | 必填；V0.6 每条配置建议且要求恰好一个来源 SO |
| tgt_functions | array(string) | 必填，至少一个目标 SO |

来源和目标必须属于同一 App 的 functions。一个来源可对应多个目标；多个来源应拆成多条依赖配置，以保持映射清晰。

### Bucket 与 Trigger

| 字段 | 类型 | 约束 |
|---|---|---|
| buckets[] | string | 当前 Registry 全局唯一 |
| bucket | string | Trigger 必填，必须在本 App buckets 中 |
| trigger_name | string | App 内唯一 |
| primitive_type | string | IMMEDIATE / BY_NAME |
| primitive.function | string | 目标 so_name，必须已注册 |
| primitive.key_name | string | BY_NAME 必填；IMMEDIATE 禁止 |
| trigger_option | integer | 0 / 1 / 2；默认 0 |

trigger_option：0=Value，1=Key+Value，2=Key Only。DIRECT 不使用 trigger_option，固定以 Value 作为目标输入。

## B.4 校验规则

1. 拒绝非法 JSON、重复字段、未知字段、错误类型和超长输入。
2. app_name 全局唯一；Bucket 跨全部 App 全局唯一。
3. so_name 必须是安全文件名，不允许路径分隔符、绝对路径和路径穿越。
4. 同一 App 内 so_name 唯一；不同 App 复用同一物理 so_name 时 resource / execution 必须完全一致。
5. 每条 DIRECT 配置必须只有一个 src_functions 项，tgt_functions 至少一个；来源和目标都必须存在。
6. Trigger 的 Bucket 和目标 SO 必须存在；trigger_name 在 App 内唯一。
7. 仅接受 IMMEDIATE / BY_NAME；ByName 必须配置非空 key_name，Immediate 禁止 key_name。
8. trigger_option 只允许 0、1、2。
9. Delay Queue enabled 必须为布尔值；时间间隔和最大等待时间必须为正整数。
10. 明确拒绝 function.json、DIRECT 以外的依赖类型、其他 Trigger Primitive 和 resource.timeout_ms。
11. 注册必须原子完成；任一校验或下游静态 Function 注册失败，不发布 AppRecord。
