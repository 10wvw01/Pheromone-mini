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
