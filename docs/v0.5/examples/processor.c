#include "task_scheduler.h"
#include <stdint.h>
#include <string.h>

/* Stateless consumer; reads the provided view and writes only via Object API. */
int handler(const task_context_t *ctx)
{
    uint32_t value;

    if (ctx == NULL ||
        ctx->input_mode == INPUT_KEY_ONLY ||
        ctx->value == NULL ||
        ctx->value_size != sizeof(value) ||
        ctx->send_object == NULL) {
        return -1;
    }

    memcpy(&value, ctx->value, sizeof(value));

    /* Example terminal output object; no trigger is required for b_result. */
    return ctx->send_object(ctx, "b_result", "processed", &value, sizeof(value));
}
