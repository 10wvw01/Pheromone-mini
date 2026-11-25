#include "task_scheduler.h"

#include <stdint.h>
#include <string.h>

/* Stateless handler. All business data exchange uses Object API. */
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
    output_value = input_value + 1U;

    /* SDK reads ctx->source_function and emits DataReadyEvent after Commit. */
    return ctx->send_object(
        ctx,
        "b_shared",
        "ready",
        &output_value,
        sizeof(output_value));
}
