# 附录 C：C 语言接口与 Task ABI

本附录是接口草案，所有标注“待接口冻结”的签名需与 Data SDK 和 Task Coroutine Pool 团队确认。代码展示结构和所有权语义，不代表现有上下游已经提供这些函数。

## C.1 基础类型

```c
#ifndef PHEROMONE_MINI_SCHEDULER_TYPES_H
#define PHEROMONE_MINI_SCHEDULER_TYPES_H

#include <stddef.h>
#include <stdint.h>

#define PM_ABI_VERSION 3u

typedef struct {
    const char *ptr;
    uint32_t len;
} pm_string_view_t;

typedef struct {
    uint32_t abi_version;
    uint32_t struct_size;
    uint64_t object_id_hi;
    uint64_t object_id_lo;
    uint64_t generation;
    uint64_t offset;
    uint64_t length;
    uint32_t region_id;
    uint32_t flags;
    pm_string_view_t owner_node_id;
} pm_data_ref_t;

typedef struct {
    uint32_t abi_version;
    uint32_t struct_size;
    uint64_t event_id_hi;
    uint64_t event_id_lo;
    pm_string_view_t app_id;
    pm_string_view_t bucket_id;
    pm_string_view_t key;
    pm_string_view_t session_id;
    pm_string_view_t source_function_id;
    pm_string_view_t target_function_id;
    pm_data_ref_t data_ref;
    uint64_t write_version;
    uint64_t commit_timestamp_ns;
    uint32_t event_type;
    uint32_t flags;
} pm_data_ready_event_t;

#endif
```

## C.2 Task 输入和描述符

```c
typedef enum {
    PM_INPUT_VALUE = 0,
    PM_INPUT_KEY_AND_VALUE = 1,
    PM_INPUT_KEY_ONLY = 2
} pm_input_mode_t;

typedef enum {
    PM_ROUTE_DIRECT = 0,
    PM_ROUTE_BUCKET_TRIGGER = 1
} pm_route_kind_t;

typedef struct {
    pm_string_view_t bucket_id;
    pm_string_view_t key;
    pm_string_view_t session_id;
    pm_data_ref_t data_ref;
    uint32_t input_mode;
    uint32_t flags;
} pm_task_input_t;

typedef struct {
    uint32_t abi_version;
    uint32_t struct_size;
    uint64_t task_id_hi;
    uint64_t task_id_lo;
    uint64_t event_id_hi;
    uint64_t event_id_lo;
    uint64_t config_generation;
    pm_string_view_t app_id;
    pm_string_view_t function_id;
    pm_string_view_t function_version;
    pm_string_view_t so_path;
    pm_string_view_t handler_symbol;
    pm_string_view_t session_id;
    pm_string_view_t trigger_id;
    uint32_t route_kind;
    uint32_t priority;
    uint64_t timeout_ms;
    const pm_task_input_t *inputs;
    uint32_t input_count;
    uint32_t flags;
    void *private_owner;
} pm_task_descriptor_t;
```

private_owner 是 Scheduler/Pool Adapter 内部所有权句柄，不允许业务 Handler 解引用。跨进程传输时必须序列化公开字段，不能传递 private_owner 指针。

## C.3 Data SDK Adapter 推荐接口

```c
typedef enum {
    PM_DATA_OK = 0,
    PM_DATA_INVALID = -1,
    PM_DATA_STALE_REF = -2,
    PM_DATA_BACKPRESSURE = -3,
    PM_DATA_UNAVAILABLE = -4
} pm_data_status_t;

/* Validate that the object and generation are still valid. */
pm_data_status_t pm_data_ref_validate(const pm_data_ref_t *ref);

/* Acquire one reference for one Task input. */
pm_data_status_t pm_data_ref_retain(const pm_data_ref_t *ref);

/* Release one previously retained reference. */
void pm_data_ref_release(const pm_data_ref_t *ref);

/* Map/read is used by Task SDK at execution time, not by Scheduler hot path. */
pm_data_status_t pm_data_ref_map_readonly(
    const pm_data_ref_t *ref,
    const void **address,
    size_t *length);

/* Release a mapping acquired by map_readonly; reference ownership is unchanged. */
void pm_data_ref_unmap_readonly(
    const pm_data_ref_t *ref,
    const void *address);
```

## C.4 Event Trigger 与 Router 接口

```c
typedef enum {
    PM_OK = 0,
    PM_DUPLICATE_EVENT = 1,
    PM_NO_TRIGGER_MATCH = 2,
    PM_ERR_INVALID_ARGUMENT = -1,
    PM_ERR_CONFIG = -2,
    PM_ERR_DATA_REF = -3,
    PM_ERR_NO_DEPENDENCY = -4,
    PM_ERR_DEPENDENCY_TARGET_MISMATCH = -5,
    PM_ERR_NO_NODE_CAPACITY = -6,
    PM_ERR_NO_PROCESS_CAPACITY = -7,
    PM_ERR_QUEUE_FULL = -8,
    PM_ERR_POOL_STOPPING = -9,
    PM_ERR_NO_MEMORY = -10,
    PM_ERR_UNSUPPORTED_TRIGGER = -11
} pm_status_t;

typedef struct pm_event_process_result pm_event_process_result_t;
typedef struct pm_route_result pm_route_result_t;

pm_status_t pm_event_trigger_handle(
    const pm_data_ready_event_t *event,
    pm_event_process_result_t *result);

pm_status_t pm_task_router_submit(
    pm_task_descriptor_t *task,
    pm_route_result_t *result);
```

## C.5 Task Pool Adapter 推荐接口

```c
typedef enum {
    PM_POOL_ACCEPTED = 0,
    PM_POOL_QUEUE_FULL = 1,
    PM_POOL_STOPPING = 2,
    PM_POOL_INVALID = 3
} pm_pool_submit_status_t;

/*
 * ACCEPTED: ownership of task transfers to the pool.
 * Other results: ownership remains with Scheduler.
 * The pool enqueues first, then attempts to wake one worker.
 * No waiting worker does not invalidate a successful enqueue.
 */
pm_pool_submit_status_t pm_task_pool_try_submit(
    void *pool_handle,
    pm_task_descriptor_t *task,
    uint32_t *queue_depth_after);
```

## C.6 Handler ABI

```c
typedef struct {
    uint32_t abi_version;
    uint32_t struct_size;
    pm_string_view_t task_id;
    pm_string_view_t event_id;
    pm_string_view_t session_id;
    pm_string_view_t function_id;
    const pm_task_input_t *inputs;
    uint32_t input_count;
    uint64_t deadline_mono_ns;
    const volatile uint32_t *cancel_flag;
} pm_task_context_t;

typedef struct {
    uint32_t abi_version;
    uint32_t struct_size;
    int32_t status_code;
    const void *opaque_output;
} pm_task_result_t;

/* Every Task SO exports this C symbol by default. */
int task_handler(
    const pm_task_context_t *context,
    pm_task_result_t *result);
```

## C.7 Producer / Processor 伪代码

Producer：
```c
int task_handler(const pm_task_context_t *ctx, pm_task_result_t *result)
{
    /* Read the current input if any, calculate the next value. */
    /* Create output through Data SDK, write payload and commit. */
    /* Data SDK publishes DataReadyEvent only after successful commit. */
    result->status_code = 0;
    return 0;
}
```

Processor：
```c
int task_handler(const pm_task_context_t *ctx, pm_task_result_t *result)
{
    const void *data = NULL;
    size_t length = 0;

    if (ctx == NULL || ctx->input_count == 0) {
        return -1;
    }

    if (pm_data_ref_map_readonly(
            &ctx->inputs[0].data_ref, &data, &length) != PM_DATA_OK) {
        return -2;
    }

    /* Consume read-only input bytes. */
    result->status_code = 0;
    return 0;
}
```

以上示例中的 Data SDK 函数是接口占位，正式实现时应由 Task SDK 提供可链接的 API。Handler 不得自行释放 Scheduler/Pool 所持有的 DataRef，除非上下游明确采用借用引用模式。

## C.8 ABI 冻结检查

- 所有公共结构体有 abi_version 和 struct_size。
- 不跨 ABI 传递 C++ 类型、异常、STL 容器或 FILE*。
- 字符串采用 pointer + length。
- DataRef 不含进程内裸指针。
- 所有权规则在头文件注释和契约测试中一致。
- Task Pool ACCEPTED 是唯一所有权转移点。
- 同一 ABI 主版本内只允许向尾部追加可选字段；不兼容变化提升主版本。
