#include "task_scheduler.h"

#include <stdint.h>
#include <string.h>

/* The SDK injects source_function=direct_source.so from this TaskContext. */
int handler(const pm_task_context_t *ctx)
{
    uint32_t input_value;
    uint32_t output_value;

    if (ctx == NULL ||
        ctx->value == NULL ||
        ctx->value_size != sizeof(input_value) ||
        ctx->send_object == NULL) {
        return -1;
    }

    memcpy(&input_value, ctx->value, sizeof(input_value));
    output_value = input_value + 100U;

    /* This Commit follows the DIRECT dependency declared in app.json. */
    return ctx->send_object(
        ctx->object_ctx,
        "b_direct_output",
        "direct_result",
        &output_value,
        sizeof(output_value));
}
