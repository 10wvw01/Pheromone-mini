# 附录 B：单一 app.json 配置协议与示例

## B.1 配置原则

V0.5 只保留一份 app.json。应用元信息、函数清单、资源/执行元信息、Bucket 清单和 Trigger 清单统一由该文件描述，上游将完整 JSON 内容传给 register_app。

不再使用 function.json，不传 function_paths，不设置 dependencies / DIRECT，不配置 entry 或 so_path。所有函数入口固定为 handler，SO 由下游框架从 /dev/shm/<so_name> 加载。

## B.2 完整示例

文件：[examples/app.json](../examples/app.json)

~~~json
{
  "app_name": "smc_demo",
  "description": "单节点单进程数据驱动示例",
  "version": "1.0.0",
  "functions": [
    {
      "name": "producer.so",
      "description": "接收输入并生成下一阶段对象",
      "version": "1.0.0",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 128,
        "timeout_ms": 5000,
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
      "name": "processor.so",
      "description": "消费处理结果",
      "version": "1.0.0",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 256,
        "timeout_ms": 10000,
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
    "b_result"
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

说明：

- app_name 是 App Registry 的全局唯一注册 KEY，不是业务逻辑实体。
- b_shared 同时关联 Immediate 和 ByName 两条 Trigger。
- b_shared/key=ready 的一次事件会命中两条规则，产生两个独立 processor Task；不会按目标函数去重。
- Data SDK 可根据数据大小选择 INLINE 或 SHARED_MEMORY；JSON 不配置 payload_type，也不控制 1KB 阈值。
- Trigger 的 trigger_option=0 表示仅传 value；trigger_option=1 表示传 key + value。
- app.json 不描述 DIRECT 依赖，不包含 function.json 的引用。

## B.3 字段定义

### App 顶层

| 字段 | 类型 | 约束 |
|---|---|---|
| app_name | string | 必填，全局唯一，非空 |
| description | string | 可选 |
| version | string | 必填，非空 |
| functions | array | 必填，至少一个 |
| buckets | array(string) | 必填，名称唯一 |
| triggers | array(object) | 必填，可为空 |

### Function

| 字段 | 类型 | 约束 |
|---|---|---|
| name | string | 必填，安全 SO 文件名，必须含 .so |
| description | string | 可选 |
| version | string | 可选，缺省继承 App version |
| resource | object | 必填 |
| resource.cpu_cores | number | 有限且大于 0 |
| resource.memory_mb | integer | 大于 0 |
| resource.timeout_ms | integer | 大于 0 |
| resource.gpu.required | boolean | 默认 false |
| resource.gpu.device_count / memory_mb | integer | 默认 0 |
| resource.disk_mb / network_mbps | integer | 默认 0 |
| execution | object | 可选，仅元数据 |
| execution.type | string | 可选，例如 coroutine |
| execution.priority | integer | 默认 0 |
| execution.affinity | array(integer) | 默认空数组，元素非负且不重复 |
| execution.max_concurrency | integer | 默认 0 表示未指定 |
| execution.reuse | boolean | 默认 false |

### Trigger

| 字段 | 类型 | 约束 |
|---|---|---|
| bucket | string | 必填，必须已在 buckets 声明 |
| trigger_name | string | 必填，App 内唯一 |
| primitive_type | string | 仅 IMMEDIATE / BY_NAME |
| primitive.function | string | 必填，必须已在 functions 注册 |
| primitive.key_name | string | BY_NAME 必填；IMMEDIATE 禁止 |
| trigger_option | integer | 0 / 1 / 2，默认 0 |

## B.4 配置校验规则

1. 拒绝重复 JSON 字段、未知字段、类型错误、超长字符串和尾随非空白内容。
2. app_name 在 Registry 全局唯一；重复注册拒绝，不覆盖。
3. Function name 在 App 内唯一；必须是安全 SO 文件名，不允许目录、绝对路径或路径穿越。
4. Bucket name 在 App 内唯一；每条 Trigger 引用的 Bucket 必须存在。
5. Trigger name 在 App 内唯一；目标 Function 必须存在。
6. 仅接受 IMMEDIATE / BY_NAME；BY_NAME 必须配置 key_name，IMMEDIATE 不得配置。
7. trigger_option 只允许 0、1、2。
8. 拒绝 dependencies、DIRECT、function.json、entry、so_path、node_schedule、process_schedule 等 V0.5 不支持字段。
9. 注册必须原子完成；任何校验失败都不发布 AppRecord。
