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
