# 附录 B：JSON 配置协议示例

本附录给出 V0.3 推荐配置。字段是设计基线，正式编码前应通过 JSON Schema 或单元测试冻结。

## B.1 app.json

```json
{
  "schema_version": "0.3",
  "app_id": "smc-demo",
  "scheduler": {
    "node_policy": "DATA_LOCALITY_THEN_MAX_AVAILABLE",
    "process_policy": "WARM_THEN_LEAST_LOADED",
    "queue_full_policy": "BACKPRESSURE",
    "event_dedup_ttl_ms": 60000,
    "event_dedup_capacity": 100000
  },
  "buckets": [
    {
      "bucket_id": "b_intermediate",
      "data_scope": "NODE_SHARED_MEMORY"
    }
  ],
  "functions": [
    {
      "function_id": "producer"
    },
    {
      "function_id": "producer_direct"
    },
    {
      "function_id": "processor"
    }
  ],
  "dependencies": [
    {
      "type": "DIRECT",
      "source_function": "producer_direct",
      "target_functions": ["processor"]
    }
  ],
  "triggers": [
    {
      "trigger_id": "trg_intermediate_immediate",
      "type": "IMMEDIATE",
      "bucket_id": "b_intermediate",
      "target_function": "processor",
      "input_mode": "VALUE",
      "enabled": true
    }
  ]
}
```

## B.2 func.json

```json
{
  "schema_version": "0.3",
  "functions": [
    {
      "function_id": "producer",
      "so_path": "/opt/pheromone-mini/functions/producer/v1/producer.so",
      "handler_symbol": "task_handler",
      "function_version": "1",
      "resource": {
        "max_concurrency": 4,
        "memory_limit_bytes": 67108864,
        "cpu_weight": 100
      },
      "execution": {
        "timeout_ms": 30000,
        "max_retries": 0,
        "priority": 0
      }
    },
    {
      "function_id": "producer_direct",
      "so_path": "/opt/pheromone-mini/functions/producer-direct/v1/producer_direct.so",
      "handler_symbol": "task_handler",
      "function_version": "1",
      "resource": {
        "max_concurrency": 4,
        "memory_limit_bytes": 67108864,
        "cpu_weight": 100
      },
      "execution": {
        "timeout_ms": 30000,
        "max_retries": 0,
        "priority": 0
      }
    },
    {
      "function_id": "processor",
      "so_path": "/opt/pheromone-mini/functions/processor/v1/processor.so",
      "handler_symbol": "task_handler",
      "function_version": "1",
      "resource": {
        "max_concurrency": 8,
        "memory_limit_bytes": 134217728,
        "cpu_weight": 100
      },
      "execution": {
        "timeout_ms": 30000,
        "max_retries": 0,
        "priority": 0
      }
    }
  ]
}
```

## B.3 字段说明

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| schema_version | string | 是 | 配置协议版本，当前为 0.3 |
| app_id | string | 是 | 应用唯一标识 |
| scheduler.node_policy | enum | 是 | 节点选择策略 |
| scheduler.process_policy | enum | 是 | 进程选择策略 |
| scheduler.queue_full_policy | enum | 是 | 队列满处理策略 |
| buckets[].bucket_id | string | 是 | Data Bucket 逻辑名称 |
| buckets[].data_scope | enum | 是 | 当前建议 NODE_SHARED_MEMORY |
| functions[].function_id | string | 是 | 应用内 Function ID |
| dependencies[].type | enum | 是 | 当前只支持 DIRECT |
| dependencies[].source_function | string | 是 | 源 Function |
| dependencies[].target_functions | array | 是 | 一个或多个目标 Function |
| triggers[].trigger_id | string | 是 | Trigger 规则唯一 ID |
| triggers[].type | enum | 是 | 当前只支持 IMMEDIATE |
| triggers[].bucket_id | string | 是 | 监听的 Bucket |
| triggers[].target_function | string | 是 | 命中后执行的 Function |
| triggers[].input_mode | enum | 否 | VALUE / KEY_AND_VALUE / KEY_ONLY |
| functions[].so_path | string | 是 | SO 路径 |
| functions[].handler_symbol | string | 否 | 默认 task_handler |
| resource.max_concurrency | integer | 是 | Function 最大并发 |
| execution.timeout_ms | integer | 是 | 执行超时元数据 |

## B.4 校验规则

1. app.json 与 func.json 的 schema_version 必须一致。
2. app_id、function_id、bucket_id、trigger_id 不得为空。
3. Function、Bucket、Trigger ID 在各自作用域内唯一。
4. app.json 中声明的每个 Function 必须在 func.json 有且仅有一条执行定义。
5. DIRECT source 和所有 target 必须在 Function Registry 中存在。
6. ImmediateTrigger 的 bucket_id 和 target_function 必须存在。
7. V0.3 只允许 DIRECT 依赖和 IMMEDIATE Trigger；其他类型拒绝启动。
8. max_concurrency、timeout_ms 必须为正数；max_retries 不得为负数。
9. so_path 必须可解析且文件可读；Handler 符号由 Task Pool 加载时最终校验。
10. 同一 source 同时配置 DIRECT 和 Bucket Trigger 时，若无法明确区分输出路径，配置校验拒绝启动。

## B.5 设计说明

producer 与 producer_direct 是两个不同的示例源函数：producer 通过 b_intermediate 的 ImmediateTrigger 触发 processor；producer_direct 通过显式 DIRECT 依赖触发 processor。这样示例不会把同一个 source 的 DIRECT 路径和 Bucket Trigger 路径混在一起。

配置不允许在 app.json 和 func.json 之间做隐式覆盖。app.json 负责拓扑和规则，func.json 负责可执行文件与资源。
