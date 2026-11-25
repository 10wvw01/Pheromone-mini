# 附录 C：公共 C 接口与 Task ABI（V0.5 草案）

本附录定义上游 Data Bucket SDK、本模块和下游 Task Coroutine Pool 的最小 C11 对接契约。真实 DataRef 类型、Object API 和 Pool ABI 需由团队共同冻结。此处为设计草案，不代表已存在实现。

## C.1 基本类型与容量

- 所有字符串均为 NUL 结尾；超长输入拒绝，不截断。
- app_name 是全局唯一注册 KEY。
- Function 只登记 so_name，不配置目录和入口；下游固定从 /dev/shm/<so_name> 加载并查找 handler。
- DataRef 是 SDK 不透明引用，不能传进程内裸指针。
- PM_INLINE_DATA_CAP 是 TaskDescriptor 的内联数据容量，不等于 Scheduler 的阈值判断；Data SDK 仍是载体选择的唯一责任方。

## C.2 关键枚举与数据结构

~~~c
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

/* Mock / ABI placeholder only; actual DataRef is defined by Data SDK. */
typedef struct {
    uint64_t token;
} data_ref_t;

typedef enum {
    EVENT_PAYLOAD_INLINE = 0,
    EVENT_PAYLOAD_SHARED_MEMORY = 1
} event_payload_type_t;

typedef enum {
    TASK_PAYLOAD_NONE = 0,          /* KEY_ONLY */
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

/* SDK-owned event view. INLINE bytes are borrowed until notify returns. */
typedef struct {
    uint64_t event_id;
    char app_name[PM_NAME_CAP];
    char bucket[PM_BUCKET_CAP];
    char key[PM_KEY_CAP];
    char session_id[PM_SESSION_CAP];
    char source_function[PM_NAME_CAP]; /* optional; tracing only */
    event_payload_type_t payload_type;
    uint64_t data_size;

    const uint8_t *inline_data; /* EVENT_PAYLOAD_INLINE only */
    size_t inline_size;
    data_ref_t data_ref;        /* EVENT_PAYLOAD_SHARED_MEMORY only */
} data_ready_event_t;

/* Owned by TaskDescriptor; never contains an SDK-borrowed Inline pointer. */
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
    char entry_name[PM_EXEC_TYPE_CAP]; /* fixed value: "handler" */
    char function_version[PM_VERSION_CAP];
    char session_id[PM_SESSION_CAP];
    char source_function[PM_NAME_CAP];
    char trigger_name[PM_NAME_CAP];
    task_source_type_t source_type;
    task_resource_t resource;
    task_execution_t execution;
    task_input_t input;
} task_desc_t;
~~~

对 SHARED_MEMORY，task_input_t 只保存 DataRef 和长度，不保存实际 Payload。对 KEY_ONLY，payload_type 必须为 TASK_PAYLOAD_NONE。

## C.3 状态码与结果

~~~c
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
    ROUTE_QUEUE_FULL = 1,       /* not accepted; log only */
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
    trigger_item_result_t *items; /* caller-owned */
    size_t capacity;
    size_t count;                 /* required count on capacity error */
    size_t matched;
    size_t enqueued;
    size_t overflow;
    size_t failed;
} trigger_result_t;
~~~

PM_OK 表示事件处理过程完成，仍需检查逐项 route_status。零匹配是 PM_OK + matched=0。队列满属于 overflow；其他构建或 Pool 错误计入 failed。wake_one 无等待者不减少 enqueued。

## C.4 Data SDK 与 Pool Adapter

~~~c
typedef struct {
    void *data_ctx;

    /* Validate one committed Shared-Memory reference and its size. */
    int (*data_validate)(void *ctx, data_ref_t ref, uint64_t data_size);

    /* Successful retain adds exactly one reference; failure adds none. */
    int (*data_retain)(void *ctx, data_ref_t ref);

    /* Release exactly one previously retained reference. */
    void (*data_release)(void *ctx, data_ref_t ref);

    void *pool_ctx;

    /*
     * Atomic capacity check + enqueue + descriptor copy.
     * ENQUEUE_OK: Pool owns the descriptor copy and this Task's DataRef.
     * Any other result: not enqueued and no ownership transferred.
     */
    pool_enqueue_result_t (*try_enqueue)(
        void *ctx, const task_desc_t *task);

    /* Called only after ENQUEUE_OK. No waiter is not an enqueue failure. */
    pool_wake_result_t (*wake_one)(void *ctx);
} scheduler_adapters_t;
~~~

Adapter 约束：

- Data SDK 基础引用在 notify 返回前有效。
- data_validate 不增加引用；data_retain 失败不得增加引用。
- Pool try_enqueue 返回 ENQUEUE_OK 前必须复制完整 TaskDescriptor。
- Pool 拒绝时不得保存描述符或引用。
- Router 在成功后调用 wake_one；WAKE_NO_WAITER 仍表示任务已入队。
- wake_one 出错不得撤销已入队任务或释放 Pool 已接管引用。

## C.5 上游 API

~~~c
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
~~~

register_app 是唯一配置注册入口。配置中的 app_name 是全局唯一 KEY；一次调用注册一个 App。函数、Bucket、Trigger 均由同一 JSON 原子注册。app_registry_seal 后禁止继续注册；运行时通知只能查询只读索引。

event_trigger_notify 是 Data SDK 的数据事件入口，不是独立 Trigger 注册 API。事件中的 Inline 指针仅在 notify 调用期间借用；Shared-Memory DataRef 由 SDK 持有基础引用直到 notify 返回。

## C.6 TaskContext 与 handler ABI

~~~c
typedef struct task_context task_context_t;

typedef int (*object_send_fn)(
    void *object_ctx,
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
    const char *key;       /* INPUT_KEY_VALUE / INPUT_KEY_ONLY */
    const void *value;     /* INPUT_VALUE / INPUT_KEY_VALUE */
    size_t value_size;

    object_send_fn send_object; /* only approved Object API */
    void *object_ctx;
};

typedef int (*task_handler_fn)(const task_context_t *ctx);
int handler(const task_context_t *ctx);
~~~

每个 SO 必须导出无状态 handler。APP 只通过 Object API 交换数据；不允许自行使用文件 I/O 或网络 Socket。TaskContext、key/value 视图仅在 handler 调用期间有效；不得保存到全局变量或跨调用复用。

## C.7 两种数据载体的所有权

| 阶段 | INLINE | SHARED_MEMORY |
|---|---|---|
| SDK 通知 | 借用 inline_data 至 notify 返回 | 持有 DataRef 基础引用 |
| Task Builder | 为每个 Task 复制字节 | 为每个 Task retain 一份 |
| Router 入队成功 | Pool 复制描述符中的字节 | Pool 接管该份 DataRef |
| Router 入队失败 | 丢弃 Task 临时副本 | Router release 本次 retain |
| handler 执行 | 使用 TaskDescriptor 副本 | Pool map DataRef |
| handler 结束 | 释放 Task 副本 | unmap 并 release DataRef |

Inline 最大容量为 PM_INLINE_DATA_CAP。该常量用于 ABI 安全，不代表 Scheduler 根据大小选择传输方式。SDK 的 1KB 选择规则及精确边界另行冻结。
