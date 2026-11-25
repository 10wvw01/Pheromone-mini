# 附录 C：公共 C 接口与 Task ABI

本附录是 V0.6 C11 接口草案。DataRef 的真实二进制表示由 Data SDK 提供；此处的 token 仅用于表达不透明句柄，不规定其内部布局。

## C.1 基本类型

~~~c
#ifndef PHEROMONE_MINI_V06_API_H
#define PHEROMONE_MINI_V06_API_H

#include <stddef.h>
#include <stdint.h>

typedef struct pm_registry pm_registry_t;
typedef struct pm_task_context pm_task_context_t;

/* Placeholder ABI handle. Data SDK owns the real opaque representation. */
typedef struct {
    uint64_t opaque_token;
} pm_data_ref_t;

typedef enum {
    PM_OK = 0,
    PM_ERR_INVALID_ARGUMENT = -1,
    PM_ERR_CONFIG = -2,
    PM_ERR_DUPLICATE_APP = -3,
    PM_ERR_DUPLICATE_BUCKET = -4,
    PM_ERR_DUPLICATE_FUNCTION = -5,
    PM_ERR_UNKNOWN_BUCKET = -6,
    PM_ERR_UNKNOWN_FUNCTION = -7,
    PM_ERR_INVALID_DEPENDENCY = -8,
    PM_ERR_INVALID_TRIGGER = -9,
    PM_ERR_INVALID_PAYLOAD = -10,
    PM_ERR_DATA_REF = -11,
    PM_ERR_NO_MEMORY = -12,
    PM_ERR_REGISTRY_SEALED = -13,
    PM_ERR_POOL_REGISTER = -14,
    PM_ERR_INTERNAL = -15
} pm_status_t;

typedef enum {
    PM_EVENT_INLINE = 0,
    PM_EVENT_SHARED_MEMORY = 1
} pm_event_payload_type_t;

typedef enum {
    PM_INPUT_VALUE = 0,
    PM_INPUT_KEY_VALUE = 1,
    PM_INPUT_KEY_ONLY = 2
} pm_input_mode_t;

typedef enum {
    PM_TASK_PAYLOAD_NONE = 0,
    PM_TASK_PAYLOAD_INLINE = 1,
    PM_TASK_PAYLOAD_SHARED_MEMORY = 2
} pm_task_payload_type_t;

typedef enum {
    PM_POOL_ACCEPTED = 0,
    PM_POOL_NO_CAPACITY = 1,
    PM_POOL_NOT_READY = 2,
    PM_POOL_INVALID_TASK = 3,
    PM_POOL_ERROR = 4
} pm_pool_submit_result_t;

typedef enum {
    PM_ROUTE_ACCEPTED = 0,
    PM_ROUTE_DELAYED = 1,
    PM_ROUTE_NO_CAPACITY = 2,
    PM_ROUTE_DELAY_EXPIRED = 3,
    PM_ROUTE_REJECTED = 4,
    PM_ROUTE_ERROR = 5
} pm_route_result_t;

#endif
~~~

## C.2 静态 Function 配置

~~~c
typedef struct {
    double cpu_cores;
    uint64_t memory_mb;
    uint8_t gpu_required;
    uint32_t gpu_device_count;
    uint64_t gpu_memory_mb;
    uint64_t disk_mb;
    uint64_t network_mbps;
} pm_function_resource_t;

typedef struct {
    const char *type;
    int32_t priority;
    const uint32_t *affinity;
    size_t affinity_count;
    uint32_t max_concurrency;
    uint8_t reuse;
} pm_function_execution_t;

typedef struct {
    const char *so_name;
    pm_function_resource_t resource;
    pm_function_execution_t execution;
} pm_function_runtime_config_t;
~~~

不包含执行超时字段。Pool 在注册阶段接收一次，后续 Task 不重复携带。

## C.3 DataReadyEvent

~~~c
typedef struct {
    const char *bucket;
    const char *key;
    const char *session_id;

    /* Required for Function-originated writes when DIRECT routing applies.
       NULL is allowed for external SMC Actor writes. */
    const char *source_function;

    pm_event_payload_type_t payload_type;
    uint64_t data_size;

    union {
        struct {
            const void *data;
            size_t size;
        } inline_payload;

        pm_data_ref_t shared_memory;
    } payload;
} pm_data_ready_event_t;
~~~

不包含 app_name、event_id、trigger_name、target_function、trigger_option、IP 或 RPC 地址。Data SDK 在通知调用期间借用事件和 Inline 缓冲区；调用返回后可以释放 Inline 缓冲区。

## C.4 TaskInvocation

~~~c
typedef struct {
    const char *so_name;
    const char *session_id;

    pm_input_mode_t input_mode;
    const char *key;

    pm_task_payload_type_t payload_type;
    uint64_t data_size;

    union {
        struct {
            const void *data;
            size_t size;
        } inline_payload;

        pm_data_ref_t shared_memory;
    } payload;
} pm_task_invocation_t;
~~~

不包含 app_name、bucket、event_id、task_id、source_function、trigger_name、source_type、resource、execution 或 handler 名称。Pool 根据 so_name 加载 /dev/shm/<so_name>，并调用固定入口 handler。

## C.5 Adapter 与 API

~~~c
typedef struct {
    void *ctx;
    int (*validate)(
        void *ctx, pm_data_ref_t ref, uint64_t data_size);
    int (*retain)(void *ctx, pm_data_ref_t ref);
    void (*release)(void *ctx, pm_data_ref_t ref);
} pm_data_ref_adapter_t;

typedef struct {
    void *ctx;
    pm_pool_submit_result_t (*register_function)(
        void *ctx, const pm_function_runtime_config_t *config);
    pm_pool_submit_result_t (*unregister_function)(
        void *ctx, const char *so_name);
    pm_pool_submit_result_t (*try_enqueue)(
        void *ctx, const pm_task_invocation_t *task);
} pm_task_pool_adapter_t;

typedef struct {
    size_t direct_matched;
    size_t trigger_matched;
    size_t enqueued;
    size_t delayed;
    size_t rejected;
    size_t failed;
} pm_notify_result_t;

pm_status_t pm_registry_create(
    pm_registry_t **out,
    const pm_data_ref_adapter_t *data_adapter,
    const pm_task_pool_adapter_t *pool_adapter);

pm_status_t register_app(
    pm_registry_t *registry,
    const char *config_json,
    size_t config_size);

pm_status_t pm_registry_seal(pm_registry_t *registry);

pm_status_t event_trigger_notify(
    pm_registry_t *registry,
    const pm_data_ready_event_t *event,
    pm_notify_result_t *result);

void pm_registry_destroy(pm_registry_t *registry);
~~~

## C.6 Task Pool 接收语义

- register_function：注册阶段调用，传递 so_name、resource、execution；重复相同配置应幂等。
- unregister_function：仅用于 App 注册事务失败时回滚本次注册；若 SO 被其他已注册 App 引用，不得提前注销。
- try_enqueue：原子容量检查和入队。
- ACCEPTED：Pool 在返回前复制描述符和 Inline 字节，并接管一份 DataRef。
- NO_CAPACITY / NOT_READY / INVALID_TASK / ERROR：Pool 不保留指针、不复制数据、不接管 DataRef。
- Pool 内部负责队列消费和协程唤醒；本模块不调用 wake_one。

## C.7 Handler ABI

~~~c
typedef int (*pm_object_send_fn)(
    void *object_ctx,
    const char *bucket,
    const char *key,
    const void *data,
    size_t size);

struct pm_task_context {
    const char *session_id;
    pm_input_mode_t input_mode;
    const char *key;
    const void *value;
    size_t value_size;
    pm_object_send_fn send_object;
    void *object_ctx;
};

int handler(const pm_task_context_t *ctx);
~~~

每个 SO 固定导出 handler，函数无状态。TaskContext 和 value 视图仅在 handler 调用期间有效。APP 只通过 Object API 交换数据，不使用文件 I/O 或网络 Socket 作为旁路数据通道。

## C.8 所有权矩阵

| 阶段 | INLINE | SHARED_MEMORY |
|---|---|---|
| SDK 通知 | SDK 借用至 notify 返回 | SDK 持有基础引用 |
| Task Builder | 每个 Value Task 复制 | 每个 Value Task retain 一份 |
| Router | 持有 Task 副本 | 持有已 retain 的 DataRef |
| Pool ACCEPTED | Pool 复制 Inline 并接管任务 | Pool 接管该份 DataRef |
| Pool 拒绝 | Router 释放 Inline 副本 | Router release |
| Delay Queue | Router 保留副本 | Router 保留 DataRef |
| Delay 超时 | Router 释放副本 | Router release |
| handler 结束 | Pool 释放副本 | Pool unmap 并 release |

KEY_ONLY 不复制 Inline，不 retain DataRef，payload_type=PM_TASK_PAYLOAD_NONE。
