# 附录 C：公共 C 接口与 Task ABI

本附录为 V0.4 推荐接口冻结草案。实际 Data SDK 引用类型、Pool API、handler 签名尚未由双方确认。完整 C11 声明见 [task_scheduler.h](../examples/task_scheduler.h)，与第 11～13 章一致；不包含调度模块实现。

## C.1 类型与边界

- 固定长度字符串/数组，所有跨回调保存的描述符字段按值复制；字符串容量含末尾 NUL，超长拒绝。
- DataRef 以 uint64_t token 的不透明结构作为 Mock 占位，不规定实际 SDK 使用整数、region/offset 或其他句柄；真实类型冻结后更新头文件。示例无进程间 wire format。
- Resource 结构包括 cpu_cores、memory_mb、timeout_ms、gpu、disk_mb、network_mbps；Execution 包括 type、priority、affinity、max_concurrency、reuse；未指定字段采用第 5 章默认值。
- task_desc_t 只有一个 task_input_t，保留对象身份、版本、长度、DataRef 和输入模式；不是业务数据副本。

## C.2 初始化与通知

```c
int event_trigger_init(event_trigger_t **out,
                       const char *app_config_path,
                       const char *const *function_paths,
                       size_t function_count,
                       const scheduler_adapters_t *adapters);
int event_trigger_notify(event_trigger_t *engine,
                         const data_ready_event_t *event,
                         trigger_result_t *result);
void event_trigger_destroy(event_trigger_t *engine);
```

init 校验所有配置并构建只读索引，function_count=0 时允许 function_paths=NULL。validate/retain/release/try_submit 为必须提供的回调；ctx 可按 SDK/Pool 契约为空。notify 的 event 是调用者借用对象，result 缓冲区由调用者持有，容量不足先返回所需项数，无投递副作用。合法事件结果 ET_OK 不等于所有任务成功入队；详见附录 D。

## C.3 SDK 与 Pool 适配器

```c
typedef struct {
    void *data_ctx;
    int (*validate)(void *ctx, const data_ready_event_t *event);
    int (*retain)(void *ctx, data_ref_t ref);
    void (*release)(void *ctx, data_ref_t ref);
    void *pool_ctx;
    pool_submit_result_t (*try_submit)(void *ctx, const task_desc_t *task);
} scheduler_adapters_t;
```

validate 和 retain 返回 0 成功；其他值失败且不增加持有。release 为不可失败的线程安全接口。try_submit 成功表示已复制描述符并接管一份 retain，可能任务已经执行完成；任何失败表示完全未接收。Router 不自行调用池内部队列和唤醒原语；真实池若分开提供两个接口，由 Adapter 对齐接收语义。Adapter 不可在入队成功后把唤醒失败伪装成未接收。

## C.4 Handler ABI

```c
typedef int (*task_handler_fn)(const task_context_t *ctx);
int handler(const task_context_t *ctx);
```

Pool 加载固定 SO 后，按 input_mode 构造借用 TaskContext：

| 模式 | key | value / value_size |
|---|---|---|
| INPUT_VALUE | NULL | 只读映射及长度 |
| INPUT_KEY_VALUE | 原 key | 只读映射及长度 |
| INPUT_KEY_ONLY | 原 key | NULL / 0 |

其他上下文包括 task_id、event_id、app_name、session_id、function_name、SDK 上下文和 write_output 回调。ctx 与所有视图有效至本次 handler 返回。池负责映射/解映射与最终 release，不把映射指针写入 Scheduler 队列。

输出回调为业务示例提出的 SDK 适配签名，非声称上游已有该 API：
```c
typedef task_write_result_t (*task_write_fn)(
    const task_context_t *ctx, const char *bucket, const char *key,
    const char *target, const void *payload, size_t size);
```

回调在返回前同步消费业务 payload，写共享内存并 Commit 后调用 notify；source/session/app 从 ctx 获得。target=NULL 表示未指定 DIRECT 目标。返回 TASK_WRITE_OK 表示数据已 Commit 且全部匹配项入队或无匹配；TASK_WRITE_NOT_COMMITTED 表示未 Commit；TASK_WRITE_COMMITTED_UNACCEPTED 表示 Commit 已成功但事件/投递存在失败（SDK 必须保留详细 trigger_result 供错误诊断）。后者不能直接重写数据或全量重放。Handler 返回 0 成功，非零失败，Pool 清理输入引用。

## C.5 完整业务示例

[producer.c](../examples/producer.c)：
```c
#include "task_scheduler.h"
#include <string.h>

int handler(const task_context_t *ctx)
{
    uint32_t value;
    if (ctx == NULL || ctx->input_mode == INPUT_KEY_ONLY ||
        ctx->value == NULL || ctx->value_size != sizeof(value) ||
        ctx->write_output == NULL)
        return -1;

    memcpy(&value, ctx->value, sizeof(value));
    if (value == UINT32_MAX) return -2;
    ++value;

    /* Source is producer.so from ctx; target NULL fans out DIRECT edges. */
    task_write_result_t rc = ctx->write_output(
        ctx, "direct_invoc", "result", NULL, &value, sizeof(value));
    /* A committed-but-unaccepted output is not safe to blindly rewrite. */
    return rc == TASK_WRITE_OK ? 0 : -(10 + (int)rc);
}
```

[processor.c](../examples/processor.c)：
```c
#include "task_scheduler.h"
#include <inttypes.h>
#include <stdio.h>
#include <string.h>

int handler(const task_context_t *ctx)
{
    uint32_t value;
    if (ctx == NULL || ctx->input_mode == INPUT_KEY_ONLY ||
        ctx->value == NULL || ctx->value_size != sizeof(value))
        return -1;

    memcpy(&value, ctx->value, sizeof(value));
    printf("processor: value=%" PRIu32 "\n", value);
    return 0;
}
```

示例使用同进程 uint32_t 字节表示，输入长度必须正确；不假定跨机器编码。外部 Actor 向 b_input 写整数，Immediate 调度 producer；producer 通过 SDK 写 direct_invoc 输出触发 DIRECT processor。独立 ByName 测试向 b_named/input 写整数直接触发 processor。业务 handler 不保存可变全局状态，不直接调用 Scheduler 内部函数。

## C.6 编译、部署与 ABI 检查

以下是实施时的示例命令（从仓库根目录执行，部署前需有权限及安全策略允许）：
```sh
# 可先在普通可执行文件系统上验证编译与导出符号
mkdir -p build/examples
cc -std=c11 -Wall -Wextra -Werror -fPIC -shared \
  docs/examples/producer.c -o build/examples/producer.so
cc -std=c11 -Wall -Wextra -Werror -fPIC -shared \
  docs/examples/processor.c -o build/examples/processor.so
nm -D --defined-only build/examples/producer.so | grep ' handler$'
nm -D --defined-only build/examples/processor.so | grep ' handler$'
# 部署团队先确认挂载权限与目录安全
findmnt -T /dev/shm/tasks
# 目录由部署团队创建并控制权限，停机后部署
install -m 0755 build/examples/producer.so /dev/shm/tasks/producer.so
install -m 0755 build/examples/processor.so /dev/shm/tasks/processor.so
```

如果 /dev/shm 为 noexec，真实 dlopen 可能失败；由环境负责人按安全要求解决，不擅自改路径或挂载选项。正式联调需核对相同 C 编译器 ABI/架构、结构体布局、签名、SO 路径、符号与生命周期。当前示例不包含 Pool loader、真实 Data SDK 或调度核心实现，不能独立运行完整业务链路。
