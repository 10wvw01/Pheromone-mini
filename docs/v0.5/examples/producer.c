#include "task_scheduler.h"
#include <stdint.h>
#include <string.h>

/*
 * Stateless example:
 * - input is provided by Task Coroutine Pool through TaskContext;
 * - output uses only the approved Object API;
 * - no file I/O, network Socket, or mutable global state.
 */
int handler(const task_context_t *ctx)
{
    uint32_t value;

    if (ctx == NULL ||
        ctx->value == NULL ||
        ctx->value_size != sizeof(value) ||
        ctx->send_object == NULL) {
        return -1;
    }

    memcpy(&value, ctx->value, sizeof(value));
    if (value == UINT32_MAX) {
        return -2;
    }

    ++value;

    /* SDK inherits app/session/source context and emits DataReadyEvent after Commit. */
    return ctx->send_object(ctx, "b_shared", "ready", &value, sizeof(value));
}
