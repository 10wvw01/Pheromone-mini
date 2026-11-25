# 附录 B：JSON 配置协议示例

## B.1 原始附件（保留原字段、层次与示例值）

来源：FUNCTION_CONFIG_JSON.json。附件实际包含两段说明文字及两个 JSON 对象。下面分别展示原始对象，未替换其字段或数值；文件名按 V0.4 要求统一为 function.json（附件称 func.json）。两个原始示例相互独立，不是能够直接联合加载的一对配置。

### 原始 app.json

```json
{
  "app_name": "Hamburger_pipeline",
  "description": "汉堡制作流水线",
  "version": "1.0.0",
  "functions": [
    {
      "name": "bread.so",
      "description": "面包片制作",
      "resource": {
        "cpu_cores": 1.0,
        "memory_mb": 512,
        "timeout_ms": 30000
      }
    },
    {
      "name": "beef.so",
      "description": "牛肉饼处理",
      "resource": {
        "cpu_cores": 2.0,
        "memory_mb": 1024,
        "timeout_ms": 10000
      }
    },
    {
      "name": "tomato.so",
      "description": "番茄片处理",
      "resource": {
        "cpu_cores": 1.5,
        "memory_mb": 768,
        "timeout_ms": 30000
      }
    },
    {
      "name": "hbg_collector.so",
      "description": "汉堡材料汇总制作",
      "resource": {
        "cpu_cores": 0.5,
        "memory_mb": 128,
        "timeout_ms": 5000
      }
    }
  ],
  "dependencies": [
    {
      "src_functions": ["bread.so"],
      "tgt_functions": ["beef.so"],
      "type": "DIRECT"
    },
    {
      "src_functions": ["beef.so"],
      "tgt_functions": ["tomato.so"],
      "type": "DIRECT"
    },
    {
      "src_functions": ["tomato.so"],
      "tgt_functions": ["hbg_collector.so"],
      "type": "DIRECT"
    }
  ],
  "triggers": [
    {
      "bucket": "b_hbg_collector",
      "trigger_name": "t_immediate_collector",
      "primitive_type": "IMMEDIATE",
      "primitive": {
        "function": "hbg_collector.so"
      },
      "trigger_option": 0
    }
  ],
  "buckets": [
    "b_hbg_collector"
  ]
}
```

### 原始单函数 function.json

```json
{
  "name": "bread_processor.so",
  "description": "面包片制作",
  "version": "1.2.0",
  "resource": {
    "cpu_cores": 2.0,
    "memory_mb": 1024,
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
    "type": "thread",
    "priority": 0,
    "affinity": [0, 1, 2, 3],
    "max_concurrency": 4,
    "reuse": true
  },
  "dependencies": {
    "produces_to": ["bread.so"],
    "depends_on": ["bread.so"]
  },
  "triggers": {
    "listen_buckets": ["b_bread_processor"],
    "conditions": []
  }
}
```

原单函数示例 bread_processor.so 未在上面的 app.functions 声明，且 b_bread_processor 不属于上例 buckets，因此按 V0.4 的关联规则不能直接把这两段作为同一应用合并；不能为追求解析通过而悄悄修改附件。原 app 中 tomato.so 有 DIRECT 出边，若它向 b_hbg_collector 写临时输出，仅 DIRECT 生效；外部或无 DIRECT 的源写此 Bucket 才触发其 Immediate。

## B.2 实际 Demo 配置（可联合加载）

- [app.json](../examples/app.json)：注册 producer.so、processor.so，展示两种 Trigger 和 DIRECT。
- [function.json](../examples/function.json)：单个 processor.so 的详细配置，按 name 补充/覆盖；不引入第二套拓扑。无需为 producer 另外提供配置。

### app.json

```json
{
  "app_name": "smc_demo",
  "description": "单进程数据驱动示例",
  "version": "1.0.0",
  "functions": [
    {
      "name": "producer.so",
      "description": "将输入整数加一并输出",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 128,
        "timeout_ms": 5000
      }
    },
    {
      "name": "processor.so",
      "description": "消费整数",
      "resource": {
        "cpu_cores": 1,
        "memory_mb": 256,
        "timeout_ms": 10000
      }
    }
  ],
  "dependencies": [
    {
      "src_functions": [
        "producer.so"
      ],
      "tgt_functions": [
        "processor.so"
      ],
      "type": "DIRECT"
    }
  ],
  "triggers": [
    {
      "bucket": "b_input",
      "trigger_name": "t_immediate_producer",
      "primitive_type": "IMMEDIATE",
      "primitive": {
        "function": "producer.so"
      },
      "trigger_option": 0
    },
    {
      "bucket": "b_named",
      "trigger_name": "t_byname_processor",
      "primitive_type": "BY_NAME",
      "primitive": {
        "function": "processor.so",
        "key_name": "input"
      },
      "trigger_option": 0
    }
  ],
  "buckets": [
    "b_input",
    "b_named"
  ]
}
```

### function.json

```json
{
  "name": "processor.so",
  "description": "消费整数",
  "version": "1.2.0",
  "resource": {
    "cpu_cores": 2,
    "memory_mb": 1024,
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
    "type": "thread",
    "priority": 0,
    "affinity": [
      0,
      1,
      2,
      3
    ],
    "max_concurrency": 4,
    "reuse": true
  },
  "dependencies": {
    "produces_to": [],
    "depends_on": [
      "producer.so"
    ]
  },
  "triggers": {
    "listen_buckets": [
      "b_named"
    ],
    "conditions": []
  }
}
```

对应行为：Actor 写 b_input → Immediate producer.so → producer 输出 direct_invoc → DIRECT processor.so；Actor 写 b_named 且 key=input → ByName processor.so。外部 Actor 写 b_named 其他 key 不触发。两个 Bucket 分开是为了独立测试两种 Primitive。direct_invoc 为内置逻辑名称，不在 buckets 中重复声明。

## B.3 合并后静态信息

| 字段 | producer.so | processor.so |
|---|---|---|
| so_path | /dev/shm/tasks/producer.so | /dev/shm/tasks/processor.so |
| entry | handler | handler |
| version | 1.0.0 | 1.2.0 |
| cpu_cores | 1.0 | 2.0 |
| memory_mb | 128 | 1024 |
| timeout_ms | 5000 | 10000 |
| execution.type | 未指定 | thread（保留配置元信息） |
| execution.max_concurrency | 未指定 | 4（仅元数据） |

function.json 不改变实际协程执行方式；app.json 决定 DIRECT 与 Trigger 拓扑。字段定义、合法值、默认值和覆盖规则见[第 5、6 章](../02-核心模块详细设计.md)。V0.4 只保留真实需求，不引入 scheduler 节点/进程策略或把 function.json 改成函数数组。
