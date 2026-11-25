#ifndef PHEROMONE_MINI_TASK_SCHEDULER_H
#define PHEROMONE_MINI_TASK_SCHEDULER_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#define PM_NAME_CAP 128u
#define PM_BUCKET_CAP 128u
#define PM_KEY_CAP 256u
#define PM_SESSION_CAP 128u
#define PM_SO_NAME_CAP 128u
#define PM_VERSION_CAP 64u
#define PM_EXEC_TYPE_CAP 32u
#define PM_AFFINITY_CAP 32u
#define PM_INLINE_DATA_CAP 1024u

typedef struct app_registry app_registry_t;
typedef struct task_context task_context_t;

/* Placeholder only. Data SDK owns the actual opaque DataRef representation. */
typedef struct {
    uint64_t token;
} data_ref_t;

typedef enum {
    EVENT_PAYLOAD_INLINE = 0,
    EVENT_PAYLOAD_SHARED_MEMORY = 1
} event_payload_type_t;

typedef enum {
    TASK_PAYLOAD_NONE = 0,
    TASK_PAYLOAD_INLINE = 1,
    TASK_PAYLOAD_SHARED_MEMORY = 2
} task_payload_type_t;

typedef enum {
    INPUT_VALUE = 0,
    INPUT_KEY_VALUE = 1,
    INPUT_KEY_ONLY = 2
} task_input_mode_t;

typedef enum {
    TASK_SOURCE_IMMEDIATE = 0,
    TASK_SOURCE_BY_NAME = 1
} task_source_type_t;

typedef struct {
    bool required;
    uint32_t device_count;
    uint64_t memory_mb;
} task_gpu_resource_t;

typedef struct {
    double cpu_cores;
    uint64_t memory_mb;
    uint32_t timeout_ms;
    task_gpu_resource_t gpu;
    uint64_t disk_mb;
    uint64_t network_mbps;
} task_resource_t;

typedef struct {
    char type[PM_EXEC_TYPE_CAP];
    int32_t priority;
    uint32_t affinity[PM_AFFINITY_CAP];
    uint32_t affinity_count;
    uint32_t max_concurrency; /* 0 = unspecified */
    bool reuse;
} task_execution_t;

/* Event memory is borrowed by notify; INLINE bytes must remain valid until it returns. */
typedef struct {
    uint64_t event_id;
    char app_name[PM_NAME_CAP];
    char bucket[PM_BUCKET_CAP];
    char key[PM_KEY_CAP];
    char session_id[PM_SESSION_CAP];
    char source_function[PM_NAME_CAP]; /* optional, tracing only */
    event_payload_type_t payload_type;
    uint64_t data_size;
    const uint8_t *inline_data;
    size_t inline_size;
    data_ref_t data_ref;
} data_ready_event_t;

/* Owned copy embedded in a TaskDescriptor; never stores inline_data from the event. */
typedef struct {
    char bucket[PM_BUCKET_CAP];
    char key[PM_KEY_CAP];
    char session_id[PM_SESSION_CAP];
    task_input_mode_t input_mode;
    task_payload_type_t payload_type;
    uint64_t data_size;
    size_t inline_size;
    uint8_t inline_data[PM_INLINE_DATA_CAP];
    data_ref_t data_ref;
} task_input_t;

typedef struct {
    uint64_t task_id;
    uint64_t event_id;
    char app_name[PM_NAME_CAP];
    char function_name[PM_NAME_CAP];
    char so_name[PM_SO_NAME_CAP];
    char entry_name[PM_EXEC_TYPE_CAP]; /* fixed value: handler */
    char function_version[PM_VERSION_CAP];
    char session_id[PM_SESSION_CAP];
    char source_function[PM_NAME_CAP];
    char trigger_name[PM_NAME_CAP];
    task_source_type_t source_type;
    task_resource_t resource;
    task_execution_t execution;
    task_input_t input;
} task_desc_t;

typedef enum {
    PM_OK = 0,
    PM_ERR_INVALID_ARGUMENT = -1,
    PM_ERR_CONFIG = -2,
    PM_ERR_DUPLICATE_APP = -3,
    PM_ERR_UNKNOWN_APP = -4,
    PM_ERR_UNKNOWN_BUCKET = -5,
    PM_ERR_UNKNOWN_FUNCTION = -6,
    PM_ERR_INVALID_PAYLOAD = -7,
    PM_ERR_DATA_REF = -8,
    PM_ERR_RESULT_CAPACITY = -9,
    PM_ERR_NO_MEMORY = -10,
    PM_ERR_REGISTRY_SEALED = -11,
    PM_ERR_INTERNAL = -12
} pm_status_t;

typedef enum {
    ENQUEUE_OK = 0,
    ENQUEUE_QUEUE_FULL = 1,
    ENQUEUE_NOT_READY = 2,
    ENQUEUE_RESOURCE_UNAVAILABLE = 3,
    ENQUEUE_INVALID_TASK = 4,
    ENQUEUE_INTERNAL_ERROR = 5
} pool_enqueue_result_t;

typedef enum {
    WAKE_SIGNALED = 0,
    WAKE_NO_WAITER = 1,
    WAKE_ERROR = 2
} pool_wake_result_t;

typedef enum {
    ROUTE_LOCAL_ENQUEUED = 0,
    ROUTE_QUEUE_FULL = 1,
    ROUTE_POOL_UNAVAILABLE = 2,
    ROUTE_INVALID_TASK = 3,
    ROUTE_ERROR = 4,
    ROUTE_NOT_ATTEMPTED = 5
} task_route_result_t;

typedef struct {
    char trigger_name[PM_NAME_CAP];
    char target_function[PM_NAME_CAP];
    task_source_type_t source_type;
    uint64_t task_id;
    pm_status_t build_status;
    task_route_result_t route_status;
    pool_wake_result_t wake_status;
} trigger_item_result_t;

typedef struct {
    trigger_item_result_t *items;
    size_t capacity;
    size_t count;
    size_t matched;
    size_t enqueued;
    size_t overflow;
    size_t failed;
} trigger_result_t;

typedef struct {
    void *data_ctx;
    int (*data_validate)(void *ctx, data_ref_t ref, uint64_t data_size);
    int (*data_retain)(void *ctx, data_ref_t ref);
    void (*data_release)(void *ctx, data_ref_t ref);

    void *pool_ctx;
    /* OK copies the descriptor and takes ownership of its DataRef, if any.
       Any rejection means no enqueue and no ownership transfer. */
    pool_enqueue_result_t (*try_enqueue)(void *ctx, const task_desc_t *task);
    /* Called only after try_enqueue returns ENQUEUE_OK. */
    pool_wake_result_t (*wake_one)(void *ctx);
} scheduler_adapters_t;

int app_registry_create(app_registry_t **out,
                        const scheduler_adapters_t *adapters);
int register_app(app_registry_t *registry,
                 const char *config_json,
                 size_t config_size);
int app_registry_seal(app_registry_t *registry);
int event_trigger_notify(app_registry_t *registry,
                         const data_ready_event_t *event,
                         trigger_result_t *result);
void app_registry_destroy(app_registry_t *registry);

/* The only business data output path exposed to APP is the Object API adapter. */
typedef int (*object_send_fn)(
    const task_context_t *ctx,
    const char *bucket,
    const char *key,
    const void *data,
    size_t size);

struct task_context {
    uint64_t task_id;
    uint64_t event_id;
    const char *app_name;
    const char *function_name;
    const char *session_id;
    task_input_mode_t input_mode;
    const char *key;
    const void *value;
    size_t value_size;
    object_send_fn send_object;
};

typedef int (*task_handler_fn)(const task_context_t *ctx);
int handler(const task_context_t *ctx);

#endif
