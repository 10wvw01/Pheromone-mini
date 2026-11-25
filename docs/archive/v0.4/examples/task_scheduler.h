#ifndef PHEROMONE_MINI_TASK_SCHEDULER_H
#define PHEROMONE_MINI_TASK_SCHEDULER_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

/* V0.4 proposed in-process C ABI. Confirm with SDK / Pool before implementation. */
#define PM_NAME_CAP 128u
#define PM_BUCKET_CAP 128u
#define PM_KEY_CAP 256u
#define PM_SESSION_CAP 128u
#define PM_PATH_CAP 256u
#define PM_ENTRY_CAP 16u
#define PM_VERSION_CAP 64u
#define PM_EXEC_TYPE_CAP 32u
#define PM_AFFINITY_CAP 32u

typedef struct event_trigger event_trigger_t;

/* Placeholder opaque value: actual representation to be frozen with Data SDK. */
typedef struct { uint64_t token; } data_ref_t;

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

typedef enum {
    INPUT_VALUE = 0,
    INPUT_KEY_VALUE = 1,
    INPUT_KEY_ONLY = 2
} task_input_mode_t;

typedef enum {
    TASK_SOURCE_DIRECT = 0,
    TASK_SOURCE_IMMEDIATE = 1,
    TASK_SOURCE_BY_NAME = 2
} task_source_type_t;

typedef struct {
    uint64_t event_id;
    char app_name[PM_NAME_CAP];
    char bucket[PM_BUCKET_CAP];
    char key[PM_KEY_CAP];
    char session_id[PM_SESSION_CAP];
    char src_function[PM_NAME_CAP];
    char tgt_function[PM_NAME_CAP];
    uint64_t object_version;
    uint64_t data_size;
    data_ref_t data_ref;
} data_ready_event_t;

typedef struct {
    char bucket[PM_BUCKET_CAP];
    char key[PM_KEY_CAP];
    char session_id[PM_SESSION_CAP];
    uint64_t object_version;
    uint64_t data_size;
    data_ref_t data_ref;
    task_input_mode_t input_mode;
} task_input_t;

typedef struct {
    uint64_t task_id;
    uint64_t event_id;
    char app_name[PM_NAME_CAP];
    char task_name[PM_NAME_CAP];
    char so_path[PM_PATH_CAP];
    char entry[PM_ENTRY_CAP];
    char version[PM_VERSION_CAP];
    task_resource_t resource;
    task_execution_t execution;
    task_source_type_t source_type;
    char src_function[PM_NAME_CAP];
    char trigger_name[PM_NAME_CAP];
    task_input_t input;
} task_desc_t;

typedef enum {
    ET_OK = 0,
    ET_ERR_INVALID_ARGUMENT = -1,
    ET_ERR_CONFIG = -2,
    ET_ERR_UNKNOWN_APP = -3,
    ET_ERR_UNKNOWN_BUCKET = -4,
    ET_ERR_UNKNOWN_SOURCE = -5,
    ET_ERR_INVALID_DEPENDENCY = -6,
    ET_ERR_DATA_REF = -7,
    ET_ERR_RESULT_CAPACITY = -8,
    ET_ERR_NO_MEMORY = -9,
    ET_ERR_INTERNAL = -10
} et_status_t;

typedef enum {
    POOL_SUBMIT_OK = 0,
    POOL_QUEUE_FULL = 1,
    POOL_NOT_READY = 2,
    POOL_RESOURCE_UNAVAILABLE = 3,
    POOL_INVALID_TASK = 4,
    POOL_INTERNAL_ERROR = 5
} pool_submit_result_t;

typedef enum {
    ROUTE_LOCAL_OK = 0,
    ROUTE_PROCESS_REQUIRED = 1, /* not accepted, log only */
    ROUTE_POOL_UNAVAILABLE = 2,
    ROUTE_INVALID_TASK = 3,
    ROUTE_ERROR = 4,
    ROUTE_NOT_ATTEMPTED = 5
} route_result_t;

typedef struct {
    char target_function[PM_NAME_CAP];
    char trigger_name[PM_NAME_CAP];
    task_source_type_t source_type;
    uint64_t task_id;
    et_status_t status;  /* build status; ET_OK if build succeeded */
    route_result_t route;
} trigger_item_result_t;

typedef struct {
    trigger_item_result_t *items; /* caller-owned buffer */
    size_t capacity;
    size_t count; /* on ET_ERR_RESULT_CAPACITY: required count */
    size_t matched;
    size_t enqueued;
    size_t overflow;
    size_t failed;
} trigger_result_t;

typedef struct {
    void *data_ctx;
    int (*validate)(void *ctx, const data_ready_event_t *event);
    int (*retain)(void *ctx, data_ref_t ref);
    void (*release)(void *ctx, data_ref_t ref);
    void *pool_ctx;
    /* OK: copied descriptor and taken its retained reference.
       Non-OK: no enqueue, no saved descriptor/reference. */
    pool_submit_result_t (*try_submit)(void *ctx, const task_desc_t *task);
} scheduler_adapters_t;

int event_trigger_init(event_trigger_t **out,
                       const char *app_config_path,
                       const char *const *function_paths,
                       size_t function_count,
                       const scheduler_adapters_t *adapters);
int event_trigger_notify(event_trigger_t *engine,
                         const data_ready_event_t *event,
                         trigger_result_t *result);
void event_trigger_destroy(event_trigger_t *engine);

/* Handler execution ABI proposal; all views borrowed until handler returns. */
typedef struct task_context task_context_t;
typedef enum {
    TASK_WRITE_OK = 0, /* committed; all matched tasks accepted or no match */
    TASK_WRITE_NOT_COMMITTED = 1,
    TASK_WRITE_COMMITTED_UNACCEPTED = 2 /* committed; some dispatch failed */
} task_write_result_t;

/* SDK adapter copies/consumes payload synchronously, commits then notifies.
   ctx supplies app/session/source; target == NULL means no explicit target. */
typedef task_write_result_t (*task_write_fn)(
    const task_context_t *ctx, const char *bucket, const char *key,
    const char *target, const void *payload, size_t size);

struct task_context {
    uint64_t task_id;
    uint64_t event_id;
    const char *app_name;
    const char *session_id;
    const char *function_name;
    task_input_mode_t input_mode;
    const char *key;       /* NULL in INPUT_VALUE */
    const void *value;     /* NULL in INPUT_KEY_ONLY */
    size_t value_size;     /* 0 in INPUT_KEY_ONLY */
    task_write_fn write_output;
    void *sdk_context;     /* SDK-owned, only interpreted by callback */
};

typedef int (*task_handler_fn)(const task_context_t *ctx);
int handler(const task_context_t *ctx);

#endif
