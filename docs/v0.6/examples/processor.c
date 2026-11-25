#include "task_scheduler.h"

#include <stdint.h>
#include <string.h>

/* Trigger example: receives Value or Key+Value according to trigger_option. */
int handler(const pm_task_context_t *ctx)
{
    uint32_t input_value;
    uint32_t output_value;

    if (ctx == NULL ||
        ctx->input_mode == PM_INPUT_KEY_ONLY ||
        ctx->value == NULL ||
        ctx->value_size != sizeof(input_value) ||
        ctx->send_object == NULL) {
        return -1;
    }

    memcpy(&input_value, ctx->value, sizeof(input_value));
    output_value = input_value * 2U;

    return ctx->send_object(
        ctx,
        "b_result",
        "processed",
        &output_value,
        sizeof(output_value));
}
