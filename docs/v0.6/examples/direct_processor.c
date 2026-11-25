#include "task_scheduler.h"

#include <stdint.h>
#include <string.h>

/* DIRECT tasks receive the source Function output as INPUT_VALUE. */
int handler(const pm_task_context_t *ctx)
{
    uint32_t direct_value;

    if (ctx == NULL ||
        ctx->input_mode != PM_INPUT_VALUE ||
        ctx->value == NULL ||
        ctx->value_size != sizeof(direct_value)) {
        return -1;
    }

    memcpy(&direct_value, ctx->value, sizeof(direct_value));

    /* Example terminal consumer. */
    (void)direct_value;
    return 0;
}
